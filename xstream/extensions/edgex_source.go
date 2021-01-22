package extensions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/edgexfoundry/go-mod-core-contracts/models"
	"github.com/emqx/kuiper/xstream/api"
	"github.com/go-zeromq/zmq4"
)

type EdgexSource struct {
	client     zmq4.Socket
	subscribed bool
	topic      string
	uri        string
	valueDescs map[string]string
}

func (es *EdgexSource) Configure(device string, props map[string]interface{}) error {
	var protocol = "tcp"
	if p, ok := props["protocol"]; ok {
		protocol = p.(string)
	}
	var server = "localhost"
	if s, ok := props["server"]; ok {
		server = s.(string)
	}
	var port = 5563
	if p, ok := props["port"]; ok {
		port = p.(int)
	}

	es.topic = ""
	if tpc, ok := props["topic"]; ok {
		es.topic = tpc.(string)
	}

	es.uri = fmt.Sprintf("%s://%s:%v", protocol, server, port)
	es.client = zmq4.NewSub(context.Background())

	return nil
}

func (es *EdgexSource) Open(ctx api.StreamContext, consumer chan<- api.SourceTuple, errCh chan<- error) {
	log := ctx.GetLogger()

	if err := es.client.Dial(es.uri); err != nil {
		info := fmt.Errorf("Failed to connect to edgex message bus: " + err.Error())
		log.Errorf(info.Error())
		errCh <- info
		return
	}

	log.Infof("The connection to edgex messagebus is established successfully.")
	if e := es.client.SetOption(zmq4.OptionSubscribe, es.topic); e != nil {
		log.Errorf("Failed to subscribe to edgex messagebus topic %s.\n", e)
		errCh <- e
	} else {
		es.subscribed = true
		log.Infof("Successfully subscribed to edgex messagebus topic %s.", es.topic)
		for {
			msg, err := es.client.Recv()
			if err != nil {
				if !es.subscribed {
					log.Infof("Successfully closed edgex subscription.")
					return
				}
				log.Warnf("Error while receiving edgex events: %v", err)
			} else {
				for _, str := range msg.Frames {
					e := models.Event{}
					if err := e.UnmarshalJSON(str); err != nil {
						len := len(str)
						if len > 200 {
							len = 200
						}
						log.Warnf("payload %s unmarshal fail: %v", str[0:(len-1)], err)
					} else {
						result := make(map[string]interface{})
						meta := make(map[string]interface{})

						log.Debugf("receive message %s from device %s", str, e.Device)
						for _, r := range e.Readings {
							if r.Name != "" {
								if v, err := es.getValue(r, log); err != nil {
									log.Warnf("fail to get value for %s: %v", r.Name, err)
								} else {
									result[r.Name] = v
								}
								r_meta := map[string]interface{}{}
								r_meta["id"] = r.Id
								r_meta["created"] = r.Created
								r_meta["modified"] = r.Modified
								r_meta["origin"] = r.Origin
								r_meta["pushed"] = r.Pushed
								r_meta["device"] = r.Device
								meta[r.Name] = r_meta
							} else {
								log.Warnf("The name of readings should not be empty!")
							}
						}
						if len(result) > 0 {
							meta["id"] = e.ID
							meta["pushed"] = e.Pushed
							meta["device"] = e.Device
							meta["created"] = e.Created
							meta["modified"] = e.Modified
							meta["origin"] = e.Origin

							select {
							case consumer <- api.NewDefaultSourceTuple(result, meta):
								log.Debugf("send data to device node")
							case <-ctx.Done():
								return
							}
						} else {
							log.Warnf("No readings are processed for the event, so ignore it.")
						}
					}
				}
			}
		}
	}
}

func (es *EdgexSource) getValue(r models.Reading, logger api.Logger) (interface{}, error) {
	v := r.Value

	if value, err := strconv.Atoi(v); err == nil {
		logger.Debugf("name %s with type integer is %v", r.Name, value)
		return value, nil
	}

	if value, err := strconv.ParseFloat(v, 32); err == nil {
		logger.Debugf("name %s with type float is %v", r.Name, value)
		return value, nil
	}

	if value, err := strconv.ParseBool(v); err == nil {
		logger.Debugf("name %s with type bool is %v", r.Name, value)
		// As SQL can't compare bool correctly, we send it back in string format
		return v, nil
	}

	logger.Debugf("name %s with type string is %v", r.Name, v)
	return v, nil
}

func (es *EdgexSource) Close(ctx api.StreamContext) error {
	if es.subscribed {
		es.subscribed = false
		if e := es.client.Close(); e != nil {
			return e
		}
	}
	return nil
}
