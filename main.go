package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/benvanmierloo/go-vitotrol"
)

var pConf *Config
var pVitotrol *vitotrol.Session
var mqttClient mqtt.Client

var customAttrRegEx = regexp.MustCompile(
	`^([a-zA-Z0-9_]+)[-_]0x([a-fA-F0-9]{1,4})\z`)

func updateDeviceAttr(deviceName string, attrName string, value string) {
	VitotrolInit()
	attrId, ok := vitotrol.AttributesNames2IDs[attrName]

	if ok && vitotrol.AttributesRef[attrId].Access == vitotrol.ReadWrite {
		fmt.Printf("Setting %s to %s\n", attrName, value)
		ok = false
		for _, vdev := range pVitotrol.Devices {
			if vdev.DeviceName == deviceName {
				ch, err := vdev.WriteDataWait(pVitotrol, attrId, value)
				if err != nil {
					fmt.Fprintf(os.Stderr, "WriteData error: %s\n", err)

					delayedShutdown()
				}
				if err = <-ch; err != nil {
					fmt.Fprintf(os.Stderr, "WriteData failed: %s\n", err)

					delayedShutdown()
				}
				// update MQTT with the new value
				token := mqttClient.Publish(pConf.MQTT.Topic+"/"+vdev.DeviceName+"/"+attrName, 0, false, value)
				token.Wait()
				ok = true
				break
			}
		}
	} else {
		ok = false
	}

	if !ok {
		fmt.Println(fmt.Sprintf("Device %s: cannot set attribute %s to %s", deviceName, attrName, value))
	}
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	if pConf != nil {
		var topicRegEx = regexp.MustCompile(pConf.MQTT.Topic + `\/(.*?)\/([^/]*?)\/set`)
		m := topicRegEx.FindStringSubmatch(msg.Topic())
		if m != nil {
			updateDeviceAttr(m[1], m[2], string(msg.Payload()))
		}
	}
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("MQTT Connected.")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("MQTT Connect lost: %v", err)

	delayedShutdown()
}

func VitotrolInit() {
	pVitotrol = &vitotrol.Session{}
	fmt.Println("Vitotrol login...")
	err := pVitotrol.Login(pConf.Vitotrol.Login, pConf.Vitotrol.Password)
	if err != nil {
		err = fmt.Errorf("Login failed: %s", err)

		delayedShutdown()

	}
	fmt.Println("Vitotrol GetDevices...")
	err = pVitotrol.GetDevices()
	if err != nil {
		err = fmt.Errorf("GetDevices failed: %s", err)

		delayedShutdown()
	}
	if len(pVitotrol.Devices) == 0 {
		err = fmt.Errorf("No device found")

		delayedShutdown()

	}
	fmt.Printf("%d device(s) found\n", len(pVitotrol.Devices))
}

func getAttrValue(vdev *vitotrol.Device, attrID vitotrol.AttrID) (value interface{}) {
	value, _ = vitotrol.AttributesRef[attrID].
		Type.Vitodata2NativeValue(vdev.Attributes[attrID].Value)

	// uint64 handled from influx 1.4
	if vuint64, ok := value.(uint64); ok {
		value = int(vuint64)
	}
	return
}

func refreshDevice(device *vitotrol.Device, attrs []vitotrol.AttrID) bool {
	ch, err := device.RefreshDataWait(pVitotrol, attrs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "RefreshData error: %s\n", err)

		delayedShutdown()
	}
	if err = <-ch; err != nil {
		fmt.Fprintf(os.Stderr, "RefreshData failed: %s\n", err)
		delayedShutdown()
	}

	err = device.GetData(pVitotrol, attrs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetData error: %s\n", err)
		delayedShutdown()
	}

	fields := map[string]interface{}{}

	for _, attrID := range attrs {
		fields[vitotrol.AttributesRef[attrID].Name] = getAttrValue(device, attrID)
	}

	// Write the batch
	values, _ := json.Marshal(fields)
	fmt.Sprintln("%", values)
	for key, element := range fields {
		token := mqttClient.Publish(pConf.MQTT.Topic+"/"+device.DeviceName+"/"+key, 0, false, fmt.Sprint(element))
		token.Wait()
	}

	return true
}

func refreshDevices() {
	fmt.Println("Refreshing fields...")

	start := time.Now()
	for _, device := range pVitotrol.Devices {
		// Check if this device has a configuration
		deviceConfig := pConf.GetConfigDevice(device.DeviceName, device.LocationName)
		if deviceConfig != nil {
			if device.IsConnected {
				refreshDevice(&device, deviceConfig.attrs)
			} else {
				// Device is not connect - retry.
				fmt.Fprintf(os.Stderr, "Device is not connected `%s'\n", device.DeviceName)
				delayedShutdown()
			}
		}
	}
	delta := time.Duration(pConf.Vitotrol.Frequency)*time.Second - time.Since(start)
	if delta > 0 {
		time.Sleep(delta)
	}
}

func resolveFields() {
	for _, configDevice := range pConf.Devices {
		attrs := make(map[vitotrol.AttrID]struct{}, len(configDevice.Fields))

		for _, fieldName := range configDevice.Fields {
			// Computed attribute
			if computedAttr, ok := computedAttrs[fieldName]; ok {
				for _, attrID := range computedAttr.Attrs {
					attrs[attrID] = struct{}{}
				}
				configDevice.computedFields = append(configDevice.computedFields, fieldName)
			} else {
				// Already known attribute
				attrID, ok := vitotrol.AttributesNames2IDs[fieldName]
				if !ok {
					// Custom attribute

					m := customAttrRegEx.FindStringSubmatch(fieldName)
					if m == nil {
						fmt.Fprintf(os.Stderr, "Unknown attribute `%s'\n", fieldName)
						delayedShutdown()
					}

					tmpName := m[1]
					access := vitotrol.ReadWrite

					// TODO Better would be to do configDevice.GetTypeInfo and cache the result
					// and check access against the real type mapping.
					if strings.HasPrefix(tmpName, "RW") {
						access = vitotrol.ReadWrite
						fmt.Println(tmpName, " is writeable")
					}

					attrRef := vitotrol.AttrRef{
						Type:   vitotrol.TypeDouble,
						Access: access,
						Name:   tmpName,
					}
					tmpID, _ := strconv.ParseUint(m[2], 16, 16)
					attrID = vitotrol.AttrID(tmpID)

					vitotrol.AddAttributeRef(attrID, attrRef)
				}

				attrs[attrID] = struct{}{}
			}
		}

		if len(attrs) == 0 {
			fmt.Fprintf(os.Stderr, "No attributes for device %s/location %s\n",
				configDevice.Name, configDevice.Location)
			delayedShutdown()
		}

		configDevice.attrs = make([]vitotrol.AttrID, 0, len(attrs))
		for attrID := range attrs {
			configDevice.attrs = append(configDevice.attrs, attrID)
		}
	}
}

func initializeMQTTClient() {
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://" + pConf.MQTT.Broker + ":" + pConf.MQTT.Port)
	opts.SetClientID(pConf.MQTT.ClientID)
	opts.SetUsername(pConf.MQTT.Login)
	opts.SetPassword(pConf.MQTT.Password)
	opts.SetAutoReconnect(false)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	mqttClient = mqtt.NewClient(opts)

	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	//subscribe to the topic to catch the control commands

	topic := pConf.MQTT.Topic + "/#"
	fmt.Println("MQTT Subscribe: ", topic)
	token := mqttClient.Subscribe(topic, 1, messagePubHandler)
	token.Wait()
}

func mainLoop() {
	for {
		VitotrolInit()
		refreshDevices()
	}
}

func main() {
	//read configuration
	configFile := flag.String("config", "", "config file")

	flag.Parse()

	if *configFile == "" {
		fmt.Fprintln(os.Stderr, "config file is missing")
		delayedShutdown()
	}
	conf, err := ReadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot read config: %s\n", err)
		delayedShutdown()
	}
	pConf = conf

	fmt.Fprintf(os.Stdout, "Refresh interval: %ds\nRestart delay: %ds\n", pConf.Vitotrol.Frequency, pConf.Vitotrol.RetryTimeout)

	// Resolve fields
	resolveFields()

	// Create a new MQTT Client
	initializeMQTTClient()

	mainLoop()
}

func delayedShutdown() {
	time.Sleep(time.Duration(pConf.Vitotrol.RetryTimeout) * time.Second)
	os.Exit(1)
}
