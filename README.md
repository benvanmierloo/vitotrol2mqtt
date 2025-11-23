
# Viessmann™ Vitotrol™ device to MQTT

Typically used to send Viessmann™ Vitotrol™ boiler data to a MQTT broker

## Installation


```sh
go build -v
```

will generate a `vitotrol2mqtt` executable.


## Usage

Create a YAML file based on
[`vitotrol2mqtt.yml`](vitotrol2mqtt.yml), including your
credentials and the attributes you want to send to the MQTT broker.

Registered attributes can be found here:
https://github.com/maxatome/go-vitotrol/blob/master/attributes.go#L79
(field `Name`).

Note! Including field mappings that are unsupported by your Viessmann device
will cause execution to crash and exit.

### Custom attributes

If you want an attribute that is not registered, use a name like
`NAME_0xNNNN` where `NAME` is the name of the attribute, and `NNNN` is
the hexadecimal representation of the attribute ID, for example:

```
FuelConsumption_0x108d
```
You can change any attribute (if changeable) by sending a MQTT command to the /set topic of each variable with the correspondig value as RAW text. Example:

`vitotrol2mqtt/VT 200 (HO1C)/DateTime/set`  `2023-08-21 08:11:00`

You can see in the LOG File if the set was successfully. You can only change writeable attributes. Other crashes.

Note! Use `RW` naming only for fields that you have checked to be writable, see below.

You can use [`vitotrol`](https://github.com/maxatome/go-vitotrol) +
`rget all`, `bget` or `remote_attrs` actions to discover attributes and their read/write access
(german language skill needed :) ).

### Computed attributes

Note that you can provide the special attribute
`ComputedSetpointTemp`. This fake attribute is computed using several
others and corresponds to the setpoint temperature (as it appears that
this value is not available in Vitotrol™ served attributes).

## Run locally

Once your `vitotrol2mqtt.yml` is ready, you can launch:

```sh
vitotrol2mqtt -config vitotrol2mqtt.yml
```
## Docker
Alternatively, you can run the tool in a docker container. The Entrypoint is modified to do a automatic restart after 5 seconds if it craches. 

`ENTRYPOINT ["/bin/bash", "-c", "while true; do /vitotrol2mqtt -config /config/vitotrol2mqtt.yml || sleep 5; done"]`

```sh
docker-compose build --no-cache
docker-compose up 
```

That's all.

