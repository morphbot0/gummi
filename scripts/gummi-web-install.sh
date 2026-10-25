#!/bin/bash
# Puts gummi's web board in place as a systemd service. Meant to be piped
# to bash as root, so every question reads the terminal, not stdin.
set -eu

UNIT=/etc/systemd/system/gummi-web.service
ENVFILE=/etc/gummi-web.env
GUMMI=/snap/gummi-agent/current/bin/gummi

[ "$(id -u)" -eq 0 ] || { echo "run as root: gummi-agent.install-script | sudo bash -ex" >&2; exit 1; }

# The service runs as the person who ran sudo, with their home.
user=${SUDO_USER:-}
{ [ -n "$user" ] && [ "$user" != root ]; } || { echo "run through sudo as the user gummi should serve (SUDO_USER is unset or root)" >&2; exit 1; }
home=$(getent passwd "$user" | cut -d: -f6)
group=$(id -gn "$user")
[ -n "$home" ] && [ -d "$home" ] || { echo "cannot find the home directory of $user" >&2; exit 1; }
workdir=/project
[ -d "$workdir" ] || workdir=$home
preseed=""
while [ $# -gt 0 ]; do
	case "$1" in
	--preseed) [ $# -ge 2 ] || { echo "--preseed needs a file" >&2; exit 1; }; preseed=$2; shift 2 ;;
	--preseed=*) preseed=${1#--preseed=}; shift ;;
	*) echo "usage: ... | sudo bash -ex -s -- [--preseed file.yaml]" >&2; exit 1 ;;
	esac
done

ask() { # ask PROMPT -> succeeds on yes (default no)
	local a
	read -r -p "$1 [y/N] " a </dev/tty >/dev/tty 2>&1 || a=n
	case "$a" in [yY]*) return 0 ;; *) return 1 ;; esac
}

# truthy VALUE -> succeeds for true/yes/y/on/1
truthy() { case "$(printf %s "$1" | tr 'A-Z' 'a-z')" in true | yes | y | on | 1) return 0 ;; *) return 1 ;; esac; }

flags=""
authkey=""

if [ -n "$preseed" ]; then
	# A flat YAML map of `key: value` lines (# comments, optional quotes):
	# terminal, tailscale, ts_hostname, ts_tls, ts_authkey.
	[ -r "$preseed" ] || { echo "cannot read preseed file $preseed" >&2; exit 1; }
	pre() { sed -n -E "s/^$1[[:space:]]*:[[:space:]]*(.*[^[:space:]])?[[:space:]]*\$/\1/p" "$preseed" | head -n1 | sed -E "s/[[:space:]]+#.*\$//; s/^(\"(.*)\"|'(.*)')\$/\2\3/"; }
	for k in $(sed -n -E 's/^([A-Za-z_]+)[[:space:]]*:.*/\1/p' "$preseed"); do
		case "$k" in terminal | tailscale | ts_hostname | ts_tls | ts_authkey) ;; *) echo "unknown preseed key: $k" >&2; exit 1 ;; esac
	done
	if truthy "$(pre terminal)"; then flags="$flags --terminal"; fi
	if truthy "$(pre tailscale)"; then
		flags="$flags --tailscale"
		host=$(pre ts_hostname)
		[ -z "$host" ] || flags="$flags --ts-hostname $host"
		if truthy "$(pre ts_tls)"; then flags="$flags --ts-tls"; fi
		authkey=$(pre ts_authkey)
	fi
else
	[ -r /dev/tty ] || { echo "no terminal to ask questions on; use --preseed file.yaml" >&2; exit 1; }
	if ask "Enable the in-browser terminal (--terminal)? Every paired device can then run commands as '$user'"; then
		flags="$flags --terminal"
	fi
	if ask "Serve the board on your tailnet (--tailscale)?"; then
		flags="$flags --tailscale"
		read -r -p "Node name on the tailnet [gummi]: " host </dev/tty >/dev/tty 2>&1 || host=""
		[ -z "$host" ] || flags="$flags --ts-hostname $host"
		if ask "Serve HTTPS with a tailnet certificate (--ts-tls; needs MagicDNS and HTTPS enabled)?"; then
			flags="$flags --ts-tls"
		fi
		read -r -s -p "Tailscale auth key (empty = log in through a link in the service log): " authkey </dev/tty >/dev/tty 2>&1 || authkey=""
		echo >/dev/tty
	fi
fi

cat >"$UNIT" <<UNIT_EOF
[Unit]
Description=gummi web board for the LXD developer workspace
After=network-online.target

[Service]
ExecStart=$GUMMI web --addr 0.0.0.0:7879$flags
Environment="HOME=$home"
Environment="PATH=$home/.local/bin:/var/lib/workshop/sdk/opencode/bin:$home/go/bin:/var/lib/workshop/sdk/copilot/bin:/var/lib/workshop/sdk/claude-code/bin:/var/lib/workshop/sdk/claude-code/usr/bin:$home/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"
WorkingDirectory=$workdir
User=$user
Group=$group
Restart=on-failure
RestartSec=30
EnvironmentFile=/etc/gummi-web.env

[Install]
WantedBy=multi-user.target
UNIT_EOF

# The env file must exist for the unit to start; keep what is already in it.
[ -e "$ENVFILE" ] || install -m 0600 /dev/null "$ENVFILE"
if [ -n "$authkey" ]; then
	sed -i '/^TS_AUTHKEY=/d' "$ENVFILE"
	printf 'TS_AUTHKEY=%s\n' "$authkey" >>"$ENVFILE"
fi
chmod 0600 "$ENVFILE"

systemctl daemon-reload
systemctl enable gummi-web.service
systemctl restart gummi-web.service

echo "gummi-web is running on port 7879. Pair a browser with: journalctl -u gummi-web -n 50"
