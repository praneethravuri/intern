package protocol

import (
	"strings"
)

const SocketPath = "/tmp/helios.sock"

const (
	VerbSpawn     = "SPAWN"
	VerbList      = "LIST"
	VerbBroadcast = "BROADCAST"
)

const BroadcastAll = "*"

// ponytail: only broadcast framing is shared; SPAWN/LIST stay inline, low drift risk

// FormatBroadcast builds a BROADCAST wire message: "BROADCAST <target> <msg>\n".
// If target is empty, defaults to BroadcastAll.
// Ensures the message ends with exactly one newline.
func FormatBroadcast(target, msg string) string {
	if target == "" {
		target = BroadcastAll
	}
	msg = strings.TrimRight(msg, "\n")
	return "BROADCAST " + target + " " + msg + "\n"
}

// ParseBroadcast parses a BROADCAST wire message. Returns target, message, and ok.
// ok is false if the line is malformed (missing prefix, no space after target, etc).
func ParseBroadcast(line string) (target, msg string, ok bool) {
	line = strings.TrimRight(line, "\n")

	if !strings.HasPrefix(line, "BROADCAST ") {
		return "", "", false
	}

	rest := strings.TrimPrefix(line, "BROADCAST ")
	if rest == "" {
		return "", "", false
	}

	// Split on first space to separate target from message
	parts := strings.SplitN(rest, " ", 2)
	target = parts[0]

	if len(parts) < 2 {
		return "", "", false
	}

	msg = parts[1]
	return target, msg, true
}
