package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"github.com/CyCoreSystems/ari/v5"
	"github.com/CyCoreSystems/ari/v5/client/native"
	"github.com/inconshreveable/log15"
)

var (
	log = log15.New()

	bridges  = make(map[string]*ari.BridgeHandle) //Map bridgeID to the bridge itself
	channels = make(map[string]string)            //Map channelID to the endpoint
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
			if len(args) < 2 {
				log.Error("at least two endpoints required")
			} else {
				bridge := createBridge(cl)

				for _, ch := range args {
					call(cl, bridge, ch)
				}
				if len(args) == 2 {
					//call between two endpoints
				} else {
					//conference
				}
			}
		case "JoinCall":
			//todo - first arg will be bridgeid of bridge to be added to, second id is endpoint
			channel := createChannel(cl, args[1])
			if channel == nil {
				return
			}
			bridge := bridges[args[0]]
			if bridge == nil {
				log.Error("this bridge does not exist")
			} else {
				bridge.AddChannel(channel.ID())
			}
		case "ListCalls":
			for _, bridge := range bridges {
				bridgeData, _ := bridge.Data()
				log.Info("Call data: ")
				for _, channelID := range bridgeData.ChannelIDs {
					log.Info("Channel ID: ", "channelID", channels[channelID])
				}
			}
		default:
			log.Error("bad choice")
		}
	}

}

func call(cl ari.Client, bridge *ari.BridgeHandle, ch string) {
	channel := createChannel(cl, ch)
	if channel == nil {
		return
	}

	if err := channel.Dial("ARI", 15*time.Second); err != nil {
		log.Error("call failed", "error", err)
	}

	if err := bridge.AddChannel(channel.ID()); err != nil {
		log.Error("add channel failed", "error", err)
	}
}

func createChannel(cl ari.Client, endpoint string) *ari.ChannelHandle {
	//Create new channel
	channel, err := cl.Channel().Create(&ari.Key{}, ari.ChannelCreateRequest{
		Endpoint: "PJSIP/" + endpoint,
		App:      "ari-app",
	})
	if err != nil {
		log.Error("failed to create channel", "error", err)
		return nil
	}

	//Add to an existing map of channels and return to caller
	channels[channel.ID()] = "PJSIP" + endpoint
	return channel
}

func createBridge(cl ari.Client) *ari.BridgeHandle {
	//Create a new bridge handle
	bridge, err := cl.Bridge().Create(&ari.Key{}, "mixing", "")

	if err != nil {
		log.Error("failed to create bridge", "error", err)
		return nil
	}

	//Add to an existing map of bridges and return to caller
	bridges[bridge.ID()] = bridge
	return bridge
}

func readInput() (string, []string) {
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	arr := strings.Fields(strings.Replace(text, "\n", "", -1))
	return arr[0], arr[1:]
	// return "Dial", []string{"5000", "5001"} //Testing
}
