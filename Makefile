SHELL := /bin/bash

# make dep runs without sudo
dep:
	sudo apt update
	sudo apt-get -y install git screen hostapd-wpe autossh bluez iodine haveged
	sudo apt-get -y install tcpdump socat
	sudo apt-get -y install python3-pip python3-dev
	sudo apt-get -y install hostapd avahi-daemon haveged
	# before installing dnsmasq, the nameserver from /etc/resolv.conf should be saved
	# to restore after install (gets overwritten by dnsmasq package)
	cp /etc/resolv.conf /tmp/backup_resolv.conf
	sudo apt-get -y install dnsmasq
	sudo /bin/bash -c 'cat /tmp/backup_resolv.conf > /etc/resolv.conf'

	# python dependencies for HIDbackdoor
	sudo pip install pycryptodome --break-system-packages # already present on stretch
	
        # dependencies for kismet gpsd and airgeddon
	sudo apt-get -y install kismet gpsd gpsd-clients airgeddon
	sudo apt-get -y install bettercap-ui isc-dhcp-server-ldap php-cgi lighttpd-mod-webdav
	sudo apt-get -y install resolvconf ffmpeg php-fpm lighttpd-modules-dbi mmdb-bin libsox-fmt-alsa
	sudo apt-get -y install spawn-fcgi lighttpd-modules-lua gpsd-clients 
	sudo apt-get -y install libsox-fmt-oss lighttpd-doc xfonts-cyrillic tmux
        #All airgeddon tools
        #sudo apt-get -y install ettercap hostapd-mana hcxtools bully pixiewps asleap hashcat wireshark-cli mdk4 hcxdumptool  

more: 
	sudo apt-get -y install ettercap-text-only hostapd-mana hcxtools bully pixiewps asleap hashcat tshark mdk4 hcxdumptool socat 

install:
	cp /boot/armbianEnv.txt /root
	cp -r usr/local/P4wnP1 /usr/local/
	cp usr/lib/systemd/system/gpsd.socket /usr/lib/systemd/system/gpsd.socket
	#cp etc/cloud/templates/hosts.debian.tmpl /etc/cloud/templates/hosts.debian.tmpl
	cp etc/systemd/system/* /etc/systemd/system/
	cp .tmux.conf /root/
	mkdir -p /usr/share/S.o.D/kismet
	mkdir -p /usr/share/S.o.D/airgeddon
	cp usr/share/airgeddon/.* /usr/share/airgeddon/
	cp etc/kismet/* /etc/kismet
	cp usr/local/bin/* /usr/local/bin/
	cp etc/default/gpsd /etc/default/gpsd
	# careful testing
	#sudo update-rc.d dhcpcd disable
	#sudo update-rc.d dnsmasq disable
	# systemctl disable networking.service # disable network service, relevant parts are wrapped by P4wnP1 (boottime below 20 seconds)

	# reinit service daemon
	systemctl daemon-reload
	# enable services
	systemctl enable haveged
	systemctl enable avahi-daemon
	systemctl enable P4wnP1.service
	systemctl enable P4wnP12026.service
	# start services
	service P4wnP1 start
	service P4wnP12026 start

full_install:
	sudo apt update
	sudo apt-get -y install git screen hostapd-wpe autossh bluez iodine haveged
	sudo apt-get -y install tcpdump socat
	sudo apt-get -y install python3-pip python3-dev
	sudo apt-get -y install hostapd haveged avahi-daemon
        #All airgeddon tools
        #sudo apt-get -y install ettercap hostapd-mana hcxtools bully pixiewps asleap hashcat wireshark-cli mdk4 hcxdumptool 
        # before installing dnsmasq, the nameserver from /etc/resolv.conf should be saved
        # to restore after install (gets overwritten by dnsmasq package)
	cp /etc/resolv.conf /tmp/backup_resolv.conf
	sudo apt-get -y install dnsmasq
	sudo /bin/bash -c 'cat /tmp/backup_resolv.conf > /etc/resolv.conf'

        # python dependencies for HIDbackdoor
	sudo pip install pycryptodome --break-system-packages # already present on stretch

        # dependencies for kismet gpsd and airgeddon
	sudo apt-get -y install kismet gpsd gpsd-clients airgeddon
	sudo apt-get -y install bettercap-ui isc-dhcp-server-ldap php-cgi lighttpd-mod-webdav
	sudo apt-get -y install resolvconf ffmpeg php-fpm lighttpd-modules-dbi mmdb-bin libsox-fmt-alsa
	sudo apt-get -y install spawn-fcgi lighttpd-modules-lua gpsd-clients 
	sudo apt-get -y install libsox-fmt-oss lighttpd-doc xfonts-cyrillic tmux
	cp /boot/armbianEnv.txt /root/
	cp -r usr/local/P4wnP1 /usr/local/
	cp usr/lib/systemd/system/gpsd.socket /usr/lib/systemd/system/gpsd.socket
	#cp etc/cloud/templates/hosts.debian.tmpl /etc/cloud/templates/hosts.debian.tmpl
	cp etc/systemd/system/* /etc/systemd/system/
	cp .tmux.conf /root/
	mkdir -p /usr/share/S.o.D/kismet
	mkdir -p /usr/share/S.o.D/airgeddon
	cp usr/share/airgeddon/.* /usr/share/airgeddon/
	cp etc/kismet/* /etc/kismet
	cp usr/local/bin/* /usr/local/bin/
	cp etc/default/gpsd /etc/default/gpsd

        # reinit service daemon
	systemctl daemon-reload
        # enable services
	systemctl enable haveged
	systemctl enable avahi-daemon
	systemctl enable P4wnP1.service
	systemctl enable P4wnP12026.service
        # start services
	service P4wnP1 start
	service P4wnP12026 start

