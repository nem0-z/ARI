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
		action, args := readInput()
		switch action {
		case "Dial":
			Dialer(cl, args)
		case "JoinCall":
			if err := JoinBridge(cl, args[0], args[1:]); err != nil {
				log.Error("failed to join bridge", "error", err)
				continue
			}
			safeUpdateCallType(args[0])
		case "ListCalls":
			ListCalls()
		default:
			log.Error("bad choice")
		}
	}

}

//Creates the bridge, adds endpoints and starts a go routine that manages the call
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

//Deletes channels whenever they leave the bridge. In addition to that, destroys the bridge if it's free.
func manageCall(cl ari.Client, bridge *ari.BridgeHandle) {
	sub := bridge.Subscribe(ari.Events.ChannelLeftBridge)

	for {
		e := <-sub.Events()
		v := e.(*ari.ChannelLeftBridge)
		channelID := v.Channel.ID

		if _, err := bridge.Play(bridge.ID(), "sound:confbridge-leave"); err != nil {
			log.Error("failed to play leave sound", "error", err)
		}

		deleteChannelFromMaps(channelID)

		chans, err := getChannelsFromBridge(bridge.ID())
		if err != nil {
			log.Error("failed to access bridge", "error", err, "bridgeID", bridge.ID())
			return
		}

		numberOfChannels := len(chans)
		callType := safeGetCallType(bridge.ID())

		if callType == "call" || numberOfChannels < 2 {
			destroyRemainingChannels(bridge)

			if err := bridge.Delete(); err != nil {
				log.Error("failed to delete bridge", "error", err)
			}
			log.Debug("destroying bridge")

			deleteBridgeFromMaps(bridge.ID())
			return
		}
	}
}

//Originates a channel and call for each of the extensions and adds them to the bridge.
func addEndpoints(cl ari.Client, bridge *ari.BridgeHandle, exts []string) error {
	for _, ext := range exts {
		if err := originate(cl, bridge, ext); err != nil {
			return err
		}
		log.Debug("joining bridge successful", "extension", ext)
	}
	return nil
}

//Wrapper for addEndpoints that uses bridgeID as an identifier instead of bridge handle itself.
func JoinBridge(cl ari.Client, bridgeID string, ext []string) error {
	bridge := bridges[bridgeID]
	if bridge == nil {
		return errors.New("this bridge does not exist")
	}

	if err := addEndpoints(cl, bridge, ext); err != nil {
		return err
	}

	return nil
}

//Creates a channel, dials it and adds it to the bridge.
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

//Creates a channel for the PJSIP/ext endpoint, adds it to the internal maps and returns a channel handle.
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

//Creates a bridge, adds it to the internal maps and defines the initial call type in the bridge.
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

//Cleans up channels from the bridge that is about to be destroyed.
func destroyRemainingChannels(bridge *ari.BridgeHandle) {
	chans, err := getChannelsFromBridge(bridge.ID())
	if err != nil {
		log.Error("failed to access bridge", "error", err, "bridgeID", bridge.ID())
	}

	for _, chanID := range chans {
		if err := channels[chanID].Hangup(); err != nil {
			log.Error("failed to destroy the channel", "error", err)
		}
		log.Debug("channel destroyed", "chanID", chanID)
	}
}

//Deletes channel identified by channelID from the internal maps.
func deleteChannelFromMaps(channelID string) {
	delete(channels, channelID)
	delete(chanEndpoints, channelID)
}

//Deletes bridge identified by bridgeID from the internal maps.
func deleteBridgeFromMaps(bridgeID string) {
	delete(bridges, bridgeID)
	delete(callTypes, bridgeID)
}

//Returns array of channel ids from the bridge identified by bridgeID.
func getChannelsFromBridge(bridgeID string) ([]string, error) {
	bridge := bridges[bridgeID]
	if bridge == nil {
		return nil, errors.New("non existing bridge")
	}
	bridgeData, err := bridge.Data()
	if err != nil {
		return nil, errors.New("could not fetch bridge data")
	}
	return bridgeData.ChannelIDs, nil
}

//Returns call type for the bridge identified by bridgeID. Locks the access during the reading.
func safeGetCallType(bridgeID string) string {
	mu.Lock()
	defer mu.Unlock()

	return callTypes[bridgeID]
}

//Updates call type for the bridge identified by bridgeID. Locks the access during the update.
func safeUpdateCallType(bridgeID string) {
	mu.Lock()
	defer mu.Unlock()

	chans, err := getChannelsFromBridge(bridgeID)
	if err != nil {
		log.Error("failed to access bridge", "error", err, "bridgeID", bridgeID)
		return
	}

	if len(chans) > 2 {
		callTypes[bridgeID] = "conference"
	}
}

//Prints out basic info about channels in the array.
func printChannels(bridgeID string, chans []string) {
	log.Info("CALL DATA", "bridge ID", bridgeID, "call type", callTypes[bridgeID])
	for _, channelID := range chans {
		log.Info("Channel info", "endpoint", chanEndpoints[channelID], "channel ID", channelID)
	}
}

//Prints out basic info about all of the active bridges/calls.
func ListCalls() {
	if len(bridges) == 0 {
		log.Warn("No active calls")
		return
	}

	for _, bridge := range bridges {
		chans, err := getChannelsFromBridge(bridge.ID())
		if err != nil {
			log.Error("failed to access bridge", "error", err, "bridgeID", bridge.ID())
		}
		printChannels(bridge.ID(), chans)
	}
}

//Prompts user for a console input and returns it in a form of action and array of arguments.
func readInput() (action string, args []string) {
	log.Info("Enter your choice: ")
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	//Split input into an array of strings
	arr := strings.Fields(strings.Replace(text, "\n", "", -1))
	return arr[0], arr[1:]
}
