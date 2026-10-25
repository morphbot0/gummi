#!/bin/sh
# Printed by `gummi-agent.install-script`; run as
#   gummi-agent.install-script | sudo bash -ex
# Installs /etc/systemd/system/gummi-web.service and /etc/gummi-web.env.
exec cat "$SNAP/share/gummi-web-install.sh"
