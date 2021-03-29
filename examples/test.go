//package main

import (
	"context"

	"github.com/CyCoreSystems/ari/v5"
	"github.com/CyCoreSystems/ari/v5/client/native"
	"github.com/inconshreveable/log15"
)

var log = log15.New()

var bridge *ari.BridgeHandle

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// connect
	native.Logger = log

	log.Info("Connecting to ARI")
	cl, err := native.Connect(&native.Options{
		Application:  "ari-app",
		Username:     "asterisk",
		Password:     "testing123",
		URL:          "http://localhost:8088/ari",
		WebsocketURL: "ws://localhost:8088/ari/events",
	})
	if err != nil {
		log.Error("Failed to build ARI client", "error", err)
		return
	}

	// setup app

	log.Info("Listening for new calls")
	sub := cl.Bus().Subscribe(nil, "StasisStart")

	for {
		select {
		case e := <-sub.Events():
			v := e.(*ari.StasisStart)
			log.Info("Got stasis start", "channel", v.Channel.ID)
			go app(ctx, cl, cl.Channel().Get(v.Key(ari.ChannelKey, v.Channel.ID)))
		case <-ctx.Done():
			return
		}
	}
}

func app(ctx context.Context, cl ari.Client, h *ari.ChannelHandle) {
	r := ari.OriginateRequest{
		Endpoint:  "PJSIP/5001",
		Timeout:   20,
		CallerID:  "Huso",
		Context:   "sets",
		Extension: "5001",
		Priority:  1,
	}
	log.Info("OriginateRequest", "request", r)
	handle, err := h.Originate(r)
}
