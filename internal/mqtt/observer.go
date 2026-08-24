package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/logging"
	"github.com/meshcore-go/OwlShack/internal/modem"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

// publishJob is queued from the RX/heartbeat goroutines and consumed by the
// per-broker worker. Keeping the publish call off the hot path prevents a
// stalled broker from back-pressuring the modem dispatch goroutine.
type publishJob struct {
	topic   string
	payload []byte
	qos     byte
	retain  bool
}

const (
	// publishQueueDepth is the per-broker job buffer. Sized to absorb a
	// short network hiccup without touching the modem RX goroutine.
	publishQueueDepth = 256

	// publishWaitTimeout bounds how long the worker waits for paho's
	// publish token. A misbehaving broker will not freeze the worker.
	publishWaitTimeout = 5 * time.Second

	// connectWaitTimeout bounds the initial connect handshake.
	connectWaitTimeout = 10 * time.Second
)

type brokerClient struct {
	cfg      config.BrokerConfig
	clientMu sync.RWMutex
	client   paho.Client
	pubKeyHx string
	iata     string

	// Resolved (placeholder-expanded) publish topics for this broker.
	packetTopicStr string
	statusTopicStr string

	disallowed map[byte]bool
	dedup      *meshcore.DedupCache // nil when dedup disabled for this broker

	publishCh  chan publishJob
	stop       chan struct{} // closed by Observer.Stop to halt the worker + senders
	workerDone chan struct{}
	dropped    atomic.Uint64
}

func (b *brokerClient) currentClient() paho.Client {
	b.clientMu.RLock()
	c := b.client
	b.clientMu.RUnlock()
	return c
}

func (b *brokerClient) swapClient(c paho.Client) {
	b.clientMu.Lock()
	b.client = c
	b.clientMu.Unlock()
}

func (b *brokerClient) packetTopic() string { return b.packetTopicStr }

func (b *brokerClient) statusTopic() string { return b.statusTopicStr }

const (
	defaultPacketTopic = "meshcore/{iata}/{pubkey}/packets"
	defaultStatusTopic = "meshcore/{iata}/{pubkey}/status"
)

// resolveTopics expands a broker's topic templates (defaulting to the
// meshcoretomqtt-compatible "meshcore/{iata}/{pubkey}/<kind>" structure).
func resolveTopics(bcfg config.BrokerConfig, iata, pubKeyHx, origin string) (packetTopic, statusTopic string) {
	expand := func(tmpl, fallback string) string {
		if tmpl == "" {
			tmpl = fallback
		}
		return strings.NewReplacer(
			"{iata}", iata, "{IATA}", iata,
			"{pubkey}", pubKeyHx, "{PUBKEY}", pubKeyHx,
			"{publicKey}", pubKeyHx, "{PUBLIC_KEY}", pubKeyHx,
			"{name}", origin, "{NAME}", origin, "{origin}", origin,
		).Replace(tmpl)
	}

	return expand(bcfg.PacketTopic, defaultPacketTopic),
		expand(bcfg.StatusTopic, defaultStatusTopic)
}

func (b *brokerClient) isAllowed(payloadType byte) bool {
	return !b.disallowed[payloadType]
}

type Observer struct {
	radio node.MuxRadio
	id    meshcore.LocalIdentity
	stats modem.StatsProvider
	log   *slog.Logger

	cfg             config.MqttConfig
	originName      string
	pubKeyHx        string
	brokers         []*brokerClient
	packetsReceived atomic.Uint64
	floodRx         atomic.Uint64
	directRx        atomic.Uint64
	floodDups       atomic.Uint64
	directDups      atomic.Uint64
	recvErrors      *atomic.Uint64

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewObserver(cfg config.MqttConfig, name string, mux *node.RadioMux, id meshcore.LocalIdentity, stats modem.StatsProvider, recvErrors *atomic.Uint64) (*Observer, error) {
	pkHex := publicKeyHex(id)
	radio := mux.NewRadio()

	obs := &Observer{
		radio:      radio,
		id:         id,
		cfg:        cfg,
		stats:      stats,
		recvErrors: recvErrors,
		originName: name,
		pubKeyHx:   pkHex,
		log:        slog.Default().With("component", "mqtt", "observer", name),
	}

	return obs, nil
}

func (o *Observer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancel = cancel
	o.mu.Unlock()

	iata := "test"
	if o.cfg.IataCode != nil && *o.cfg.IataCode != "" {
		iata = *o.cfg.IataCode
	}

	for _, bcfg := range o.cfg.Brokers {
		if !bcfg.Enabled {
			continue
		}

		client, err := o.connectBroker(bcfg, iata)
		if err != nil {
			o.log.Error("broker connect failed", "broker", bcfg.Name, "error", err)
			continue
		}

		disallowed := parseDisallowed(bcfg.DisallowedPacketTypes)
		packetTopic, statusTopic := resolveTopics(bcfg, iata, o.pubKeyHx, o.originName)

		bc := &brokerClient{
			cfg:            bcfg,
			client:         client,
			pubKeyHx:       o.pubKeyHx,
			iata:           iata,
			packetTopicStr: packetTopic,
			statusTopicStr: statusTopic,
			disallowed:     disallowed,
			publishCh:      make(chan publishJob, publishQueueDepth),
			stop:           make(chan struct{}),
			workerDone:     make(chan struct{}),
		}
		if bcfg.Dedup {
			bc.dedup = &meshcore.DedupCache{}
		}

		go o.publishWorker(bc)

		o.publishStatus(ctx, bc, "online")
		o.brokers = append(o.brokers, bc)
		o.log.Info("connected", "broker", bcfg.Name)
	}

	o.radio.SetPacketFilter(func(_ *meshcore.Packet) bool { return true })
	o.radio.SetRawDataHandler(o.onData)

	go o.heartbeatLoop(ctx)
	go o.tokenRefreshLoop(ctx)

	return nil
}

func (o *Observer) Stop() {
	o.mu.Lock()
	if o.cancel != nil {
		o.cancel()
	}
	o.mu.Unlock()

	// Detach from the radio mux so no new packets are dispatched to onData.
	// A deliver already in-flight (the mux snapshots its radio list before
	// delivering) is still handled safely: enqueuePublish never sends on a
	// closed channel because publishCh is closed by no one.
	if o.radio != nil {
		o.radio.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, bc := range o.brokers {
		o.publishStatus(ctx, bc, "offline")
		close(bc.stop)
		select {
		case <-bc.workerDone:
		case <-time.After(publishWaitTimeout):
			o.log.Warn("publish worker did not exit cleanly", "broker", bc.cfg.Name)
		}
		bc.currentClient().Disconnect(500)
	}
	o.brokers = nil
}

// publishWorker drains a broker's publish channel serially. token.Wait() is
// bounded so a stalled broker never blocks indefinitely; the worker just
// drops the job, logs once, and moves on.
func (o *Observer) publishWorker(bc *brokerClient) {
	defer close(bc.workerDone)
	for {
		select {
		case <-bc.stop:
			// Drain whatever is already buffered (e.g. the offline status
			// enqueued during Stop) on a best-effort basis, then exit.
			for {
				select {
				case job := <-bc.publishCh:
					o.doPublish(bc, job)
				default:
					return
				}
			}
		case job := <-bc.publishCh:
			o.doPublish(bc, job)
		}
	}
}

func (o *Observer) doPublish(bc *brokerClient, job publishJob) {
	client := bc.currentClient()
	if client == nil {
		return
	}
	token := client.Publish(job.topic, job.qos, job.retain, job.payload)
	if !token.WaitTimeout(publishWaitTimeout) {
		o.log.Warn("publish timed out", "broker", bc.cfg.Name, "topic", job.topic)
		return
	}
	if err := token.Error(); err != nil {
		o.log.Error("publish error", "broker", bc.cfg.Name, "error", err)
	}
}

// enqueuePublish hands a job to the broker's worker without blocking. If the
// queue is full (broker is stalled), the job is dropped and counted. Called
// from the modem RX goroutine, so this MUST never block.
func (o *Observer) enqueuePublish(bc *brokerClient, job publishJob) {
	select {
	case bc.publishCh <- job:
	case <-bc.stop:
		// Observer is shutting down; drop silently. publishCh is never closed,
		// so this send can never panic even if a radio deliver is still
		// in-flight after Stop detaches the radio.
	default:
		dropped := bc.dropped.Add(1)
		if dropped == 1 || dropped%100 == 0 {
			o.log.Warn("publish queue full, dropping",
				"broker", bc.cfg.Name, "topic", job.topic, "dropped", dropped)
		}
	}
}

func (o *Observer) onData(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
	o.log.Log(context.Background(), logging.LevelTrace, "raw radio data",
		"len", len(data), "hex", strings.ToUpper(hex.EncodeToString(data)),
		"snr", snr, "rssi", rssi)

	pkt, err := meshcore.PacketFromBytes(data)
	if err != nil {
		o.log.Log(context.Background(), logging.LevelTrace, "packet parse failed", "error", err)
		return
	}
	pkt.SNR = snr
	pkt.RSSI = rssi
	pkt.HasSignalInfo = hasSignalInfo

	o.packetsReceived.Add(1)
	if pkt.IsRouteDirect() {
		o.directRx.Add(1)
	} else {
		o.floodRx.Add(1)
	}
	o.publishPacket(pkt, data, "rx")
}

func (o *Observer) publishPacket(pkt *meshcore.Packet, rawBytes []byte, direction string) {
	o.log.Log(context.Background(), logging.LevelTrace, "new packet accepted",
		"direction", direction, "type", pkt.PayloadType(),
		"payload_len", len(pkt.Payload))

	payload, err := formatPacket(pkt, rawBytes, o.originName, o.pubKeyHx, direction)
	if err != nil {
		o.log.Error("format error", "error", err)
		return
	}

	for _, bc := range o.brokers {
		if !bc.isAllowed(pkt.PayloadType()) {
			o.log.Log(context.Background(), logging.LevelTrace, "packet type filtered",
				"broker", bc.cfg.Name, "type", pkt.PayloadType())
			continue
		}
		if bc.dedup != nil && bc.dedup.HasSeen(pkt) {
			o.log.Log(context.Background(), logging.LevelTrace, "dedup hit, skipping",
				"broker", bc.cfg.Name, "type", pkt.PayloadType())
			if pkt.IsRouteDirect() {
				o.directDups.Add(1)
			} else {
				o.floodDups.Add(1)
			}
			continue
		}
		o.log.Log(context.Background(), logging.LevelTrace, "publishing packet",
			"broker", bc.cfg.Name, "topic", bc.packetTopic(), "direction", direction)
		o.enqueuePublish(bc, publishJob{
			topic:   bc.packetTopic(),
			payload: payload,
			qos:     0,
			retain:  false,
		})
	}
}

func (o *Observer) heartbeatLoop(ctx context.Context) {
	interval := time.Duration(o.cfg.StatusIntervalSeconds()) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, bc := range o.brokers {
				o.publishStatus(ctx, bc, "online")
			}
		}
	}
}

func (o *Observer) tokenRefreshLoop(ctx context.Context) {
	refreshAt := time.Duration(float64(tokenLifetime) * 0.8)
	ticker := time.NewTicker(refreshAt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, bc := range o.brokers {
				if !strings.EqualFold(bc.cfg.AuthType, "token") {
					continue
				}
				o.log.Debug("refreshing token", "broker", bc.cfg.Name)
				bc.currentClient().Disconnect(250)

				newClient, err := o.connectBroker(bc.cfg, bc.iata)
				if err != nil {
					o.log.Error("token refresh reconnect failed", "broker", bc.cfg.Name, "error", err)
					continue
				}
				bc.swapClient(newClient)
				o.publishStatus(ctx, bc, "online")
				o.log.Info("token refreshed", "broker", bc.cfg.Name)
			}
		}
	}
}

func (o *Observer) publishStatus(ctx context.Context, bc *brokerClient, status string) {
	var radio modem.RadioInfo
	var ds modem.DeviceStats
	if o.stats != nil {
		radio = o.stats.RadioConfig()
		ds = o.stats.Stats(ctx)
	}

	packets := PacketCounts{
		Received:   o.packetsReceived.Load(),
		FloodRx:    o.floodRx.Load(),
		DirectRx:   o.directRx.Load(),
		FloodDups:  o.floodDups.Load(),
		DirectDups: o.directDups.Load(),
	}

	payload, err := formatStatus(status, o.originName, o.pubKeyHx, radio, ds, packets, o.recvErrors.Load())
	if err != nil {
		o.log.Error("status format error", "error", err)
		return
	}

	o.log.Log(ctx, logging.LevelTrace, "publishing status",
		"broker", bc.cfg.Name, "topic", bc.statusTopic(),
		"json", string(payload))
	o.enqueuePublish(bc, publishJob{
		topic:   bc.statusTopic(),
		payload: payload,
		qos:     1,
		retain:  bc.cfg.RetainStatus,
	})
}

func (o *Observer) connectBroker(bcfg config.BrokerConfig, iata string) (paho.Client, error) {
	var scheme string
	switch strings.ToLower(bcfg.Transport) {
	case "websockets", "ws", "wss":
		if bcfg.TlsEnabled {
			scheme = "wss"
		} else {
			scheme = "ws"
		}
	default:
		if bcfg.TlsEnabled {
			scheme = "tls"
		} else {
			scheme = "tcp"
		}
	}

	brokerURL := fmt.Sprintf("%s://%s:%d%s", scheme, bcfg.Host, bcfg.Port, bcfg.Path)
	clientID := fmt.Sprintf("meshcore_%s_%s", o.pubKeyHx[:16], bcfg.Host)

	opts := paho.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(5 * time.Minute)

	if bcfg.TlsEnabled {
		opts.SetTLSConfig(&tls.Config{
			InsecureSkipVerify: bcfg.TlsInsecure,
			MinVersion:         tls.VersionTLS12,
		})
	}

	switch strings.ToLower(bcfg.AuthType) {
	case "token":
		audience := bcfg.Audience
		if audience == "" {
			audience = bcfg.Host
		}
		token, _, err := generateToken(o.id, audience, derefStr(o.cfg.Email), derefStr(o.cfg.Owner))
		if err != nil {
			return nil, fmt.Errorf("generating auth token: %w", err)
		}
		opts.SetUsername(tokenUsername(o.id))
		opts.SetPassword(token)
	case "basic":
		opts.SetUsername(bcfg.Username)
		opts.SetPassword(bcfg.Password)
	}

	_, statusTopic := resolveTopics(bcfg, iata, o.pubKeyHx, o.originName)

	// LWT uses minimal status (no live stats — we're about to disconnect).
	offlinePayload, _ := formatStatus("offline", o.originName, o.pubKeyHx, modem.RadioInfo{}, modem.DeviceStats{}, PacketCounts{}, 0)
	opts.SetWill(statusTopic, string(offlinePayload), 1, bcfg.RetainStatus)

	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(connectWaitTimeout) {
		client.Disconnect(0)
		return nil, fmt.Errorf("connecting to %s: timeout after %s", brokerURL, connectWaitTimeout)
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", brokerURL, err)
	}

	return client, nil
}

var payloadTypeNames = map[string]byte{
	"req":        meshcore.PayloadTypeReq,
	"response":   meshcore.PayloadTypeResponse,
	"txt_msg":    meshcore.PayloadTypeTxtMsg,
	"ack":        meshcore.PayloadTypeAck,
	"advert":     meshcore.PayloadTypeAdvert,
	"grp_txt":    meshcore.PayloadTypeGrpTxt,
	"grp_data":   meshcore.PayloadTypeGrpData,
	"anon_req":   meshcore.PayloadTypeAnonReq,
	"path":       meshcore.PayloadTypePath,
	"trace":      meshcore.PayloadTypeTrace,
	"multi_part": meshcore.PayloadTypeMultiPart,
	"control":    meshcore.PayloadTypeControl,
	"raw_custom": meshcore.PayloadTypeRawCustom,
}

func parseDisallowed(names []string) map[byte]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[byte]bool, len(names))
	for _, name := range names {
		if v, ok := payloadTypeNames[strings.ToLower(name)]; ok {
			m[v] = true
		}
	}
	return m
}
