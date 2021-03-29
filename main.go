package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/CyCoreSystems/ari/v5"
	"github.com/CyCoreSystems/ari/v5/client/native"
	"github.com/CyCoreSystems/ari/v5/ext/play"
	"github.com/CyCoreSystems/ari/v5/rid"
	"github.com/inconshreveable/log15"
)

var log = log15.New()

var bridge *ari.BridgeHandle

func main() {
	//Create a context and defer the cancelling until we are done with everything
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//Connect to ARI
	cl, err := native.Connect(&native.Options{
		Application:  "ari-app",
		Username:     "asterisk",
		Password:     "testing123",
		URL:          "http://localhost:8088/ari",
		WebsocketURL: "ws://localhost:8088/ari/events",
	})
	if err != nil {
		log.Error("Failed to connect to ARI", "error", err)
	}

	//Subscribe to StasisStart event
	sub := cl.Bus().Subscribe(nil, "StasisStart")

	for {
		select {
		//Case StasisStart
		case e := <-sub.Events():
			v := e.(*ari.StasisStart)
			log.Info("StasisStart", "channel", v.Channel.ID)
			//Start the app as a go routine
			go app(ctx, cl, cl.Channel().Get(v.Key(ari.ChannelKey, v.Channel.ID)))
		case <-ctx.Done():
			//End everything once the context is done
			return
		}
	}
}

func app(ctx context.Context, cl ari.Client, h *ari.ChannelHandle) {
	log.Info("App started", "channel", h.Key().ID)

	//Answer the call
	if err := h.Answer(); err != nil {
		log.Error("failed to answer", "error", err)
		return
	}
	play.Play(ctx, h, play.URI("sound:hello-world"))

	for {
		reader := bufio.NewReader(os.Stdin)
		text, _ := reader.ReadString('\n')
		input := strings.Fields(strings.Replace(text, "\n", "", -1))

		//Parse input and forward those arguments to create/manage bridge functions
		if err := createBridge(ctx, cl, h.Key()); err != nil {
			log.Error("failed to create bridge", "error", err)
		}
	}
}

func createBridge(ctx context.Context, cl ari.Client, src *ari.Key) error {
	if bridge != nil {
		//Bridge already exists, no need to create anything
		return nil
	}

	key := src.New(ari.BridgeKey, rid.New(rid.Bridge))
	bridge, err := cl.Bridge().Create(key, "mixing", key.ID)
	if err != nil {
		bridge = nil
		return errors.New("failed to create bridge")
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go manageBridge(ctx, bridge, wg)
	wg.Wait()

	return nil
}

func manageBridge(ctx context.Context, bridge *ari.BridgeHandle, wg *sync.WaitGroup) {
	//Delete the bridge once we are done
	defer bridge.Delete()

	bridgeDestroy := bridge.Subscribe(ari.Events.BridgeDestroyed)
	defer bridgeDestroy.Cancel()

	bridgeEnter := bridge.Subscribe(ari.Events.ChannelEnteredBridge)
	defer bridgeEnter.Cancel()

	bridgeLeave := bridge.Subscribe(ari.Events.ChannelLeftBridge)
	defer bridgeLeave.Cancel()

	wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-bridgeDestroy.Events():
			return
		case e := <-bridgeEnter.Events():
			v := e.(*ari.ChannelEnteredBridge)
			log.Debug("channel left bridge", "channel", v.Channel.Name)
			go playSound(ctx, bridge, "sound:confbridge-join")
			return
		case e := <-bridgeLeave.Events():
			v := e.(*ari.ChannelLeftBridge)
			log.Debug("channel left bridge", "channel", v.Channel.Name)
			go playSound(ctx, bridge, "sound:confbridge-leave")
			return
		}
	}
}

func playSound(ctx context.Context, bridge *ari.BridgeHandle, sound string) {
	play.Play(ctx, bridge, play.URI(sound))
}
