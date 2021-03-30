package main

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/CyCoreSystems/ari/v5"
	"github.com/CyCoreSystems/ari/v5/client/native"
	"github.com/inconshreveable/log15"
)

var (
	log = log15.New()

	bridges   = make(map[string]*ari.BridgeHandle) //Map bridgeID to the bridge itself
	callTypes = make(map[string]string)            //Map bridgeID to the call type (call, conference)
	channels  = make(map[string]string)            //Map channelID to the endpoint
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
			if err := JoinBridge(cl, args[0], args[1]); err != nil {
				log.Error("failed to join bridge", "error", err)
			}
			updateCallType(args[0])
		case "ListCalls":
			ListCalls()
		default:
			log.Error("bad choice")
		}
	}

}

func updateCallType(bridgeID string) {
	bridge := bridges[bridgeID]
	bridgeData, _ := bridge.Data()

	if len(bridgeData.ChannelIDs) > 2 {
		callTypes[bridge.ID()] = "conference"
	}
}

func ListCalls() {
	if len(bridges) == 0 {
		log.Warn("No active calls")
		return
	}

	for _, bridge := range bridges {
		bridgeData, _ := bridge.Data()
		log.Info("CALL DATA", "bridgeID", bridgeData.ID)
		for _, channelID := range bridgeData.ChannelIDs {
			//I like nested for loops what you gonna do?
			log.Info("Channel ID: ", "channelID", channels[channelID])
		}
	}
}

func Dialer(cl ari.Client, args []string) {
	var callType string

	if len(args) < 2 {
		log.Warn("at least two endpoints required")
	} else {
		if len(args) == 2 {
			callType = "call"
		} else {
			callType = "conference"
		}
		bridge := createBridge(cl, callType)
		if bridge == nil {
			log.Error("failed to create bridge")
			return
		}

		if err := addEndpoints(cl, bridge, args); err != nil {
			log.Error("failed to add endpoints", "error", err)
		}

		if callTypes[bridge.ID()] == "call" {
			go manageCall(cl, bridge)
		}
	}
}

func manageCall(cl ari.Client, bridge *ari.BridgeHandle) {
	sub := cl.Bus().Subscribe(nil, "StasisEnd")
	<-sub.Events()

	if _, err := bridge.Play(bridge.ID(), "sound:confbridge-leave"); err != nil {
		log.Error("failed to play leave sound", "error", err)
	}

	log.Debug("destroying bridge")
	if err := bridge.Delete(); err != nil {
		log.Error("failed to delete bridge", "error", err)
	}
	delete(bridges, bridge.ID())
	delete(callTypes, bridge.ID())

	//TODO: make sure that the other participant gets kicked out of the call
	//Should I destroy the channels?
}

func JoinBridge(cl ari.Client, bridgeID string, ext string) error {
	bridge := bridges[bridgeID]
	if bridge == nil {
		return errors.New("this bridge does not exist")
	}

	if err := originate(cl, bridge, ext); err != nil {
		return err
	}

	log.Debug("joining bridge successful")
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

	channels[channel.ID()] = "PJSIP/" + ext
	return channel, nil
}

func createBridge(cl ari.Client, callType string) *ari.BridgeHandle {
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
