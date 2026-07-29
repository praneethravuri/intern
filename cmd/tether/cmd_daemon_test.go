package main

import "testing"

func TestDaemonBanner(t *testing.T) {
	got := daemonBanner("/tmp/sock", "/tmp/tether.db")
	for _, want := range []string{
		"running the daemon in the foreground",
		"/tmp/sock",
		"/tmp/tether.db",
		"tether ls",
	} {
		requireContains(t, got, want, "banner")
	}
}
