package main

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/CyCoreSystems/ari/v5"
	"github.com/CyCoreSystems/ari/v5/client/native"
	"github.com/inconshreveable/log15"
)

var (
	log           = log15.New()
	bridges       = make(map[string]*ari.BridgeHandle)  //Map bridgeID to the bridge itself
	chanEndpoints = make(map[string]string)             //Map channelID to the endpoint (just for more simple listing of call info, not neccessary at all)
	channels      = make(map[string]*ari.ChannelHandle) //Map channelID to the channel itself
	callTypes     = make(map[string]string)             //Map bridgeID to the call type (call, conference
	mu            sync.Mutex
)

func main() {
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
	log.Info("Connected to ARI")

	for {
		log.Info("Enter your choice: ")
		action, args := readInput()
		switch action {
		case "Dial":
			Dialer(cl, args)
		case "JoinCall":
			if err := JoinBridge(cl, args[0], args[1:]); err != nil {
				log.Error("failed to join bridge", "error", err)
				continue
			}
			updateCallType(args[0])
		case "ListCalls":
			ListCalls()
			//ListChannels()
		default:
			log.Error("bad choice")
		}
	}

}

func ListChannels() {
	log.Debug("CHANNELS INFO")
	for chanID, _ := range channels {
		log.Debug("channel", "channelID", chanID)
	}
}

func getChannelsFromBridge(bridgeID string) []string {
	bridge := bridges[bridgeID]
	if bridge == nil {
		log.Error("no bridge")
		return nil
	}
	bridgeData, _ := bridge.Data()
	return bridgeData.ChannelIDs
}

func getCallType(bridgeID string) string {
	mu.Lock()
	defer mu.Unlock()

	return callTypes[bridgeID]
}

func updateCallType(bridgeID string) {
	mu.Lock()
	defer mu.Unlock()

	if len(getChannelsFromBridge(bridgeID)) > 2 {
		callTypes[bridgeID] = "conference"
	}
}

func ListCalls() {
	if len(bridges) == 0 {
		log.Warn("No active calls")
		return
	}

	for _, bridge := range bridges {
		channels := getChannelsFromBridge(bridge.ID())
		log.Info("CALL DATA", "bridge ID", bridge.ID(), "call type", callTypes[bridge.ID()])
		for _, channelID := range channels {
			//I like nested for loops what you gonna do?
			log.Info("Channel info", "endpoint", chanEndpoints[channelID], "channel ID", channelID)
		}
	}
}

func destroyRemainingChannels(bridge *ari.BridgeHandle) {
	for _, chanID := range getChannelsFromBridge(bridge.ID()) {
		if err := channels[chanID].Hangup(); err != nil {
			log.Error("failed to destroy the channel", "error", err)
		}
	}
}

func Dialer(cl ari.Client, args []string) {

	if len(args) < 2 {
		log.Warn("at least two endpoints required")
	} else {
		bridge := createBridge(cl, len(args))
		if bridge == nil {
			log.Error("failed to create bridge")
			return
		}

		if err := addEndpoints(cl, bridge, args); err != nil {
			log.Error("failed to add endpoints", "error", err)
		}

		go manageCall(cl, bridge)
	}
}

func manageCall(cl ari.Client, bridge *ari.BridgeHandle) {
	sub := bridge.Subscribe(ari.Events.ChannelLeftBridge)

	for {
		e := <-sub.Events()
		v := e.(*ari.ChannelLeftBridge)
		channelID := v.Channel.ID

		if _, err := bridge.Play(bridge.ID(), "sound:confbridge-leave"); err != nil {
			log.Error("failed to play leave sound", "error", err)
		}

		delete(channels, channelID)
		delete(chanEndpoints, channelID)
		log.Debug("destroying channel", "channelID", channelID)

		numberOfChannels := len(getChannelsFromBridge(bridge.ID()))
		callType := getCallType(bridge.ID())

		if callType == "call" || numberOfChannels < 2 {
			go destroyRemainingChannels(bridge)

			if err := bridge.Delete(); err != nil {
				log.Error("failed to delete bridge", "error", err)
			}
			log.Debug("destroying bridge")

			delete(bridges, bridge.ID())
			delete(callTypes, bridge.ID())
			return
		}
	}
}

func JoinBridge(cl ari.Client, bridgeID string, ext []string) error {
	bridge := bridges[bridgeID]
	if bridge == nil {
		return errors.New("this bridge does not exist")
	}

	for _, e := range ext {
		if err := originate(cl, bridge, e); err != nil {
			return err
		}
		log.Debug("joining bridge successful", "extension", e)
	}

	return nil
}

func addEndpoints(cl ari.Client, bridge *ari.BridgeHandle, exts []string) error {
	for _, ext := range exts {
		if err := originate(cl, bridge, ext); err != nil {
			return err
		}
	}
	return nil
}

func originate(cl ari.Client, bridge *ari.BridgeHandle, ch string) error {
	channel, err := createChannel(cl, ch)
	if err != nil {
		log.Error("failed to create channel", "error", err)
		return err
	}

	if err := channel.Dial("ARI", 10*time.Second); err != nil {
		log.Error("dial failed", "error", err)
		return err
	}

	if err := bridge.AddChannel(channel.ID()); err != nil {
		log.Error("add channel failed", "error", err)
		return err
	}

	return nil
}

func createChannel(cl ari.Client, ext string) (*ari.ChannelHandle, error) {
	channel, err := cl.Channel().Create(&ari.Key{}, ari.ChannelCreateRequest{
		Endpoint: "PJSIP/" + ext,
		App:      "ari-app",
	})
	if err != nil {
		return nil, err
	}

	chanEndpoints[channel.ID()] = "PJSIP/" + ext
	channels[channel.ID()] = channel
	return channel, nil
}

func createBridge(cl ari.Client, numberOfChannels int) *ari.BridgeHandle {
	var callType string

	if numberOfChannels == 2 {
		callType = "call"
	} else {
		callType = "conference"
	}

	bridge, err := cl.Bridge().Create(&ari.Key{}, "mixing", "")
	if err != nil {
		log.Error("failed to create bridge", "error", err)
		return nil
	}

	bridges[bridge.ID()] = bridge
	callTypes[bridge.ID()] = callType
	return bridge
}

func readInput() (action string, args []string) {
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	//Split input into an array of strings
	arr := strings.Fields(strings.Replace(text, "\n", "", -1))
	return arr[0], arr[1:]
}
