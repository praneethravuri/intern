.PHONY: build daemon stop

build:
	go build -o heliosd ./cmd/heliosd
	go build -o helios ./cmd/helios

daemon: build
	./heliosd > /tmp/heliosd.log 2>&1 & echo $$! > /tmp/heliosd.pid
	@sleep 1
	@echo "heliosd running (pid $$(cat /tmp/heliosd.pid)), logs at /tmp/heliosd.log"

stop:
	@kill $$(cat /tmp/heliosd.pid) 2>/dev/null && echo "stopped" || echo "not running"
	@rm -f /tmp/heliosd.pid
