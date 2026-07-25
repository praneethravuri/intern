.PHONY: build daemon stop

build:
	go build -o tetherd ./cmd/tetherd
	go build -o tether ./cmd/tether

daemon: build
	./tetherd > /tmp/tetherd.log 2>&1 & echo $$! > /tmp/tetherd.pid
	@sleep 1
	@echo "tetherd running (pid $$(cat /tmp/tetherd.pid)), logs at /tmp/tetherd.log"

stop:
	@kill $$(cat /tmp/tetherd.pid) 2>/dev/null && echo "stopped" || echo "not running"
	@rm -f /tmp/tetherd.pid
