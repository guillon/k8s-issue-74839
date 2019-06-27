.PHONY: all image docker-issue-1090-server


SERVER_HOST=$(shell hostname)
SERVER_PORT=9000

all: k8s-issue-74839

docker-issue-1090-server: k8s-issue-74839
	sudo ./k8s-issue-74839 $(SERVER_PORT)

docker-issue-1090-client:
	# Client inside a NATed docker
	docker run -it alpine sh -c 'set -x; while true; do nc -w 3 $(SERVER_HOST) $(SERVER_PORT); sleep 1; done'

docker-issue-1090-client-ok:
	# Client on host
	set -x; while true; do nc -w 3 $(SERVER_HOST) $(SERVER_PORT); sleep 1; done

k8s-issue-74839: main.go tcp.go
	go build

image: k8s-issue-74839
	docker build . -t anfernee/k8s-issue-74839


