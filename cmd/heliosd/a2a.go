package main

import (
	"net/http"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"go.uber.org/zap"
)

// a2aAddr is the TCP loopback address the A2A Agent Card is served on.
const a2aAddr = "127.0.0.1:47531"

// serveA2A serves the Agent Card at the well-known path so a genuinely external
// A2A-native agent -- one that isn't any of Helios's 5 target harnesses, all of
// which reach the mailbox via CLI or MCP instead -- can discover heliosd. Card
// only: the a2asrv.RequestHandler that would actually serve message/send over
// HTTP is real work, left for when something A2A-native needs it (see the v1
// build spec artifact and [[Messaging Bus Pivot]] in the vault).
func serveA2A(log *zap.SugaredLogger) {
	card := heliosBusAgentCard("http://" + a2aAddr)
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	log.Infof("A2A agent card at http://%s%s", a2aAddr, a2asrv.WellKnownAgentCardPath)
	if err := http.ListenAndServe(a2aAddr, mux); err != nil {
		log.Warnw("A2A listener stopped", "error", err)
	}
}

func heliosBusAgentCard(url string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "Helios Bus",
		Description:        "Routes a message to a named agent registered with heliosd's mailbox.",
		URL:                url,
		Version:            "0.1.0",
		ProtocolVersion:    "0.3.0",
		PreferredTransport: a2a.TransportProtocolJSONRPC,
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities:       a2a.AgentCapabilities{},
		Skills: []a2a.AgentSkill{
			{
				ID:          "send-message",
				Name:        "Send message",
				Description: "Deliver a message to a named agent registered with the Helios mailbox.",
				Tags:        []string{"messaging"},
			},
		},
	}
}
