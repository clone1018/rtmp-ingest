package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Glimesh/rtmp-ingest/pkg/protocols/ftl"

	"github.com/Glimesh/rtmp-ingest/pkg/orchestrator"
	"github.com/Glimesh/rtmp-ingest/pkg/services/glimesh"

	"net/http"
	_ "net/http/pprof"

	"github.com/sirupsen/logrus"
)

func main() {
	log := logrus.New()
	log.Level = logrus.DebugLevel

	var streamManager StreamManager

	hostname, err := os.Hostname()
	if err != nil {
		// How tf
		log.Fatal(err)
	}

	// Should use viper or something in the future
	glimeshService := glimesh.New(glimesh.Config{
		Address:      os.Getenv("RTMP_INGEST_GLIMESH_ADDRESS"),
		ClientID:     os.Getenv("RTMP_INGEST_GLIMESH_CLIENT_ID"),
		ClientSecret: os.Getenv("RTMP_INGEST_GLIMESH_CLIENT_SECRET"),
	})
	err = glimeshService.Connect()
	if err != nil {
		log.Fatal(err)
	}

	orchTransport, err := net.Dial("tcp", os.Getenv("RTMP_INGEST_ORCHESTRATOR_ADDRESS"))
	if err != nil {
		log.Fatal(err)
	}
	orch := orchestrator.NewClient(orchestrator.Config{
		RegionCode: "global",
		Hostname:   hostname,
		Logger:     log.WithFields(logrus.Fields{"app": "orchestrator"}),
		Callbacks: orchestrator.Callbacks{
			OnStreamRelaying: func(message orchestrator.StreamRelayingMessage) {
				if message.Context == 1 {
					log.Infof("Starting relay for %d to %s", message.ChannelID, message.TargetHostname)
					streamManager.RelayMedia(message.ChannelID, message.TargetHostname, ftl.DefaultPort, message.StreamKey)
				} else {
					log.Infof("Removing relay for %d to %s", message.ChannelID, message.TargetHostname)
					streamManager.StopRelay(message.ChannelID, message.TargetHostname)
				}
			},
		},
	})
	if err := orch.Connect(orchTransport); err != nil {
		log.Fatal(err)
	}

	streamManager = NewStreamManager(orch, glimeshService)

	closeHandler(orch, streamManager)

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	// Blocking call to start the RTMP server
	NewRTMPServer(streamManager, log.WithFields(logrus.Fields{"app": "rtmp"}))
}

func closeHandler(orch orchestrator.Client, streamManager StreamManager) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-c

		// Stop all streams we're handling
		for k := range streamManager.streams {
			streamManager.StopStream(k)
		}

		// Tell orchestrator goodbye for now
		orch.Close()

		os.Exit(0)
	}()
}
