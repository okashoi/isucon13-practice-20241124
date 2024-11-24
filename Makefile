.PHONY: *

gogo: stop-services build truncate-logs start-services

stop-services:
	sudo systemctl stop dnsdist.service
	sudo systemctl stop pdns.service
	sudo systemctl stop nginx
	ssh isucon-s2 "sudo systemctl stop isupipe-go.service"
	sudo systemctl stop isupipe-go.service
	sudo systemctl stop mysql
	ssh isucon-s3 "sudo systemctl stop mysql"

build:
	cd go/ && make build
	scp go/isupipe isucon-s2:/home/isucon/webapp/go   

truncate-logs:
	sudo journalctl --vacuum-size=1K
	sudo truncate --size 0 /var/log/nginx/access.log
	sudo truncate --size 0 /var/log/nginx/error.log
	sudo truncate --size 0 /var/log/mysql/mysql-slow.log && sudo chmod 666 /var/log/mysql/mysql-slow.log
	ssh isucon-s3 "sudo truncate --size 0 /var/log/mysql/mysql-slow.log && sudo chmod 666 /var/log/mysql/mysql-slow.log"
	sudo truncate --size 0 /var/log/mysql/error.log
	ssh isucon-s3 "sudo truncate --size 0 /var/log/mysql/error.log"

start-services:
	sudo systemctl start mysql
	ssh isucon-s3 "sudo systemctl start mysql" 
	ssh isucon-s2 "sudo systemctl start isupipe-go.service"
	sudo systemctl start isupipe-go.service
	sudo systemctl start nginx
	sudo systemctl start pdns.service
	sudo systemctl start dnsdist.service

kataribe: timestamp=$(shell TZ=Asia/Tokyo date "+%Y%m%d-%H%M%S")
kataribe:
	mkdir -p ~/kataribe-logs
	sudo cp /var/log/nginx/access.log /tmp/last-access.log && sudo chmod 0666 /tmp/last-access.log
	cat /tmp/last-access.log | kataribe -conf kataribe.toml > ~/kataribe-logs/$$timestamp.log
	cat ~/kataribe-logs/$$timestamp.log | grep --after-context 20 "Top 20 Sort By Total"

pprof: TIME=60
pprof: PROF_FILE=~/pprof.samples.$(shell TZ=Asia/Tokyo date +"%H%M").$(shell git rev-parse HEAD | cut -c 1-8).pb.gz
pprof:
	curl -sSf "http://localhost:6060/debug/fgprof?seconds=$(TIME)" > $(PROF_FILE)
	go tool pprof $(PROF_FILE)
