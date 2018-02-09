package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"

	"github.com/nsqio/go-nsq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

var (
	unmarshalErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "nsq_reception_acker_unmarshal_error_total",
			Help: "Number of messages that could not be unmarshalled from JSON.",
		},
	)
	messagesReceived = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "nsq_reception_acker_messagesReceived_total",
			Help: "Number of messages that have been received.",
		},
	)
	messagesSent = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "nsq_reception_acker_messagesSent_total",
			Help: "Number of messages that have been sent.",
		},
	)
)

func init() {
	prometheus.MustRegister(unmarshalErrors)
	prometheus.MustRegister(messagesReceived)
	prometheus.MustRegister(messagesSent)
}

func main() {
	app := kingpin.New("nsq-reception-acker", "Acknowledges messages have been received and retransmits a message.")
	lookupds := app.Flag("lookupd-http-addr", "Address and port to lookupd HTTP interface.").Required().TCPList()
	receiveTopic := app.Flag("receive-topic", "Topic to listen on for messages.").Required().String()
	receiveChannel := app.Flag("receive-channel", "Channel to listen on for messages.").Required().String()
	concurrency := app.Flag("concurrency", "Concurrency for handling incoming messages. Mostly to parallelize JSON unmarshalling.").Default(strconv.Itoa(runtime.NumCPU())).Int()
	sendAddr := app.Flag("send-addr", "Address to forward payloads to.").Default("localhost:4150").TCP()
	prometheusListen := app.Flag("prometheus-addr", "Interface which Prometheus metrics are exposed on.").Default("localhost:9415").TCP()

	kingpin.MustParse(app.Parse(os.Args[1:]))

	if *concurrency <= 1 {
		log.Fatal("'concurrency' must be positive.")
	}

	var err error

	var producer *nsq.Producer
	producer, err = nsq.NewProducer((*sendAddr).String(), nsq.NewConfig())
	if err != nil {
		log.Fatal(err)
	}

	err = producer.Ping()
	if err != nil {
		log.Fatal(err)
	}

	var consumer *nsq.Consumer
	consumer, err = nsq.NewConsumer(*receiveTopic, *receiveChannel, nsq.NewConfig())
	if err != nil {
		log.Fatal(err)
	}
	consumer.ChangeMaxInFlight(20)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe((*prometheusListen).String(), nil))
	}()

	consumer.AddConcurrentHandlers(&LoggingHandler{&Handler{producer}}, *concurrency)

	lookupdStrings := make([]string, len(*lookupds))
	for i, e := range *lookupds {
		lookupdStrings[i] = e.String()
	}
	err = consumer.ConnectToNSQLookupds(lookupdStrings)
	if err != nil {
		log.Fatal(err)
	}

	exitChan := make(chan os.Signal)
	signal.Notify(exitChan, os.Interrupt)
	<-exitChan

	consumer.Stop()
	<-consumer.StopChan
}

type LoggingHandler struct {
	Delegate nsq.Handler
}

func (h *LoggingHandler) HandleMessage(m *nsq.Message) error {
	err := h.Delegate.HandleMessage(m)
	if err != nil {
		log.Println(err)
	}
	return err
}

type Handler struct {
	Producer *nsq.Producer
}

func (h *Handler) HandleMessage(m *nsq.Message) error {
	messagesReceived.Inc()

	var e Envelope
	if err := json.Unmarshal(m.Body, &e); err != nil {
		unmarshalErrors.Inc()
		// If we can't decode, there's likely no reason we should try again later.
		return nil
	}

	// Making two PublishAsync calls here to speed up processing.
	payloadDoneChan := make(chan *nsq.ProducerTransaction, 1)
	if err := h.Producer.PublishAsync(e.PayloadDestination, []byte(e.Payload), payloadDoneChan); err != nil {
		return err
	}

	// Debatable if should return this error or not.
	ackDoneChan := make(chan *nsq.ProducerTransaction, 1)
	if err := h.Producer.PublishAsync(e.AcknowledgementTopic, []byte(e.MessageId), ackDoneChan); err != nil {
		return err
	}

	if result := <-payloadDoneChan; result.Error != nil {
		return result.Error
	}
	if result := <-ackDoneChan; result.Error != nil {
		return result.Error
	}

	messagesSent.Inc()
	return nil
}

type Envelope struct {
	Payload              string `json:"payload"`
	PayloadDestination   string `json:"payload-destination"`
	MessageId            string `json:"message-id"`
	AcknowledgementTopic string `json:"acknowledgement-topic"`
}
