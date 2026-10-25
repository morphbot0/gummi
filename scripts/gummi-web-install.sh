#!/bin/bash
# Puts gummi's web board in place as a systemd service. Meant to be piped
# to bash as root, so every question reads the terminal, not stdin.
set -eu

UNIT=/etc/systemd/system/gummi-web.service
ENVFILE=/etc/gummi-web.env
GUMMI=/snap/gummi-agent/current/bin/gummi

[ "$(id -u)" -eq 0 ] || { echo "run as root: gummi-agent.install-script | sudo bash -ex" >&2; exit 1; }
[ -r /dev/tty ] || { echo "no terminal to ask questions on" >&2; exit 1; }

ask() { # ask PROMPT -> succeeds on yes (default no)
	local a
	read -r -p "$1 [y/N] " a </dev/tty >/dev/tty 2>&1 || a=n
	case "$a" in [yY]*) return 0 ;; *) return 1 ;; esac
}

flags=""
authkey=""

if ask "Enable the in-browser terminal (--terminal)? Every paired device can then run commands as 'workshop'"; then
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

cat >"$UNIT" <<UNIT_EOF
[Unit]
Description=gummi web board for the LXD developer workspace
After=network-online.target

[Service]
ExecStart=$GUMMI web --addr 0.0.0.0:7879$flags
Environment="PATH=/home/workshop/.local/bin:/var/lib/workshop/sdk/opencode/bin:/home/workshop/go/bin:/var/lib/workshop/sdk/copilot/bin:/var/lib/workshop/sdk/claude-code/bin:/var/lib/workshop/sdk/claude-code/usr/bin:/home/workshop/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"
WorkingDirectory=/project
User=workshop
Group=workshop
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
