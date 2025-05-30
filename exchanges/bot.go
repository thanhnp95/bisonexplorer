// Copyright (c) 2019-2021, The Decred developers
// See LICENSE for details.

package exchanges

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	credentials "google.golang.org/grpc/credentials"

	dcrrates "github.com/decred/dcrdata/exchanges/v3/ratesproto"
)

const (
	// DefaultCurrency is overridden by ExchangeBotConfig.BtcIndex. Data
	// structures are cached for DefaultCurrency, so requests are a little bit
	// faster.
	DefaultCurrency = "USD"
	// DefaultDataExpiry is the amount of time between calls to the exchange API.
	DefaultDataExpiry = "5m"
	// DefaultRequestExpiry : Any data older than RequestExpiry will be discarded.
	DefaultRequestExpiry = "60m"

	defaultDCRRatesPort = "7778"

	aggregatedOrderbookKey    = "aggregated"
	aggregatedBTCOrderbookKey = "btc_aggregated"
	orderbookKey              = "depth"
	TYPEDCR                   = "dcr"
	TYPELTC                   = "ltc"
	TYPEBTC                   = "btc"
	LTCSYMBOL                 = "LTCUSDT"
	BTCSYMBOL                 = "BTCUSDT"
	DCRBTCSYMBOL              = "DCRBTC"
	DCRUSDSYMBOL              = "DCRUSD"
)

// ExchangeBotConfig is the configuration options for ExchangeBot.
// DataExpiry must be less than RequestExpiry.
// Recommend RequestExpiry > 2*DataExpiry, which will permit the exchange API
// request to fail a couple of times before the exchange's data is discarded.
type ExchangeBotConfig struct {
	Disabled       []string
	DataExpiry     string
	RequestExpiry  string
	BtcIndex       string
	LTCIndex       string
	Indent         bool
	MasterBot      string
	MasterCertFile string
	BinanceAPIURL  string
}

// ExchangeBot monitors exchanges and processes updates. When an update is
// received from an exchange, the state is updated, and some convenient data
// structures are prepared. Make ExchangeBot with NewExchangeBot.
type ExchangeBot struct {
	mtx             sync.RWMutex
	DcrBtcExchanges map[string]Exchange
	LTCUSDExchanges map[string]Exchange
	BTCUSDExchanges map[string]Exchange
	IndexExchanges  map[string]Exchange
	Exchanges       map[string]Exchange
	LTCExchanges    map[string]Exchange
	BTCExchanges    map[string]Exchange
	versionedCharts map[string]*versionedChart
	chartVersions   map[string]int
	// BtcIndex is the (typically fiat) currency to which the DCR price should be
	// converted by default. Other conversions are available via a lookup in
	// indexMap, but with slightly lower performance.
	// 3-letter currency code, e.g. USD.
	BtcIndex     string
	BTCIndex     string
	LTCIndex     string
	indexMap     map[string]FiatIndices
	currentState ExchangeBotState
	// Both currentState and stateCopy hold the same information. currentState
	// is updated by ExchangeBot, and a copy stored in stateCopy. After creation,
	// stateCopy will never be updated, so can be used read-only by multiple
	// threads.
	stateCopy         *ExchangeBotState
	currentStateBytes []byte
	DataExpiry        time.Duration
	RequestExpiry     time.Duration
	minTick           time.Duration
	// A gRPC connection
	masterConnection *grpc.ClientConn
	TLSCredentials   credentials.TransportCredentials
	// Channels requested by the user.
	updateChans []chan *ExchangeUpdate
	indexChans  []chan *IndexUpdate
	quitChans   []chan struct{}
	// exchangeChan and indexChan are passed to the individual exchanges and
	// receive updates after a refresh is triggered.
	exchangeChan chan *ExchangeUpdate
	indexChan    chan *IndexUpdate
	client       *http.Client
	config       *ExchangeBotConfig
	// The failed flag is set when there are either no up-to-date Bitcoin-fiat
	// exchanges or no up-to-date Decred exchanges. IsFailed is a getter for failed.
	failed bool
}

// ExchangeBotState is the current known state of all exchanges, in a certain
// base currency, and a volume-averaged price and total volume in DCR.
type ExchangeBotState struct {
	BtcIndex          string                    `json:"btc_index"`
	BtcPrice          float64                   `json:"btc_fiat_price"`
	Price             float64                   `json:"price"`
	DCRUSD24hChange   float64                   `json:"dcr_usd_24h_change"`
	LowPrice          float64                   `json:"low_price"`
	HighPrice         float64                   `json:"high_price"`
	DCRBTCPrice       float64                   `json:"dcr_btc_price"`
	DCRBTC24hChange   float64                   `json:"dcr_btc_24h_change"`
	Volume            float64                   `json:"volume"`
	DCRBTCVolume      float64                   `json:"dcr_btc_volume"`
	LTCPrice          float64                   `json:"ltc_price"`
	LTCPriceChange    float64                   `json:"ltc_price_change"`
	LTCLowPrice       float64                   `json:"ltc_low_price"`
	LTCHighPrice      float64                   `json:"ltc_high_price"`
	LTCVolume         float64                   `json:"ltc_volume"`
	BTCPrice          float64                   `json:"btc_price"`
	BTCPriceChange    float64                   `json:"btc_price_change"`
	BTCUSDPriceChange float64                   `json:"btc_usd_price_change"`
	BTCLowPrice       float64                   `json:"btc_low_price"`
	BTCHighPrice      float64                   `json:"btc_high_price"`
	BTCVolume         float64                   `json:"btc_volume"`
	DcrBtc            map[string]*ExchangeState `json:"dcr_btc_exchanges"`
	LtcUsd            map[string]*ExchangeState `json:"ltc_usd_exchanges"`
	BtcUsd            map[string]*ExchangeState `json:"btc_usd_exchanges"`
	// FiatIndices:
	// TODO: We only really need the BaseState for the fiat indices.
	FiatIndices   map[string]*ExchangeState `json:"btc_indices"`
	VolumnOrdered []*TokenedExchange
}

type ExchangeBotStateContent struct {
	BtcIndex      string                    `json:"btc_index"`
	BtcPrice      float64                   `json:"btc_fiat_price"`
	Price         float64                   `json:"price"`
	LowPrice      float64                   `json:"low_price"`
	HighPrice     float64                   `json:"high_price"`
	Volume        float64                   `json:"volume"`
	Change24h     float64                   `json:"change_24h"`
	ExchangeState map[string]*ExchangeState `json:"exchange_state"`
	// FiatIndices:
	// TODO: We only really need the BaseState for the fiat indices.
	FiatIndices   map[string]*ExchangeState `json:"btc_indices"`
	VolumnOrdered []*TokenedExchange
}

func (state ExchangeBotStateContent) BtcToFiat(btc float64) float64 {
	return state.BtcPrice * btc
}

// Copy an ExchangeState map.
func copyStates(m map[string]*ExchangeState) map[string]*ExchangeState {
	c := make(map[string]*ExchangeState)
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Creates a pointer to a copy of the ExchangeBotState.
func (state ExchangeBotState) copy() *ExchangeBotState {
	state.DcrBtc = copyStates(state.DcrBtc)
	state.LtcUsd = copyStates(state.LtcUsd)
	state.BtcUsd = copyStates(state.BtcUsd)
	state.FiatIndices = copyStates(state.FiatIndices)
	return &state
}

// BtcToFiat converts an amount of Bitcoin to fiat using the current calculated
// exchange rate.
func (state *ExchangeBotState) BtcToFiat(btc float64) float64 {
	return state.BtcPrice * btc
}

// FiatToBtc converts an amount of fiat in the default index to a value in BTC.
func (state *ExchangeBotState) FiatToBtc(fiat float64) float64 {
	if state.BtcPrice == 0 {
		return -1
	}
	return fiat / state.BtcPrice
}

// ExchangeState doesn't have a Token field, so if the states are returned as a
// slice (rather than ranging over a map), a token is needed.
type TokenedExchange struct {
	Token string
	State *ExchangeState
}

// VolumeOrderedExchanges returns a list of tokenedExchange sorted by volume,
// highest volume first.
func (state *ExchangeBotState) VolumeOrderedExchanges() []*TokenedExchange {
	xcList := make([]*TokenedExchange, 0, len(state.DcrBtc))
	for token, state := range state.DcrBtc {
		state.Sticks = state.StickList()
		xcList = append(xcList, &TokenedExchange{
			Token: token,
			State: state,
		})
	}
	sort.Slice(xcList, func(i, j int) bool {
		if xcList[i].Token == "binance" {
			return true
		}
		if xcList[j].Token == "binance" {
			return false
		}
		return xcList[i].State.Volume > xcList[j].State.Volume
	})
	return xcList
}

func (state *ExchangeBotState) MutilchainVolumeOrderedExchanges(chainType string) []*TokenedExchange {
	currentChainState := state.GetMutilchainExchangeState(chainType)
	xcList := make([]*TokenedExchange, 0, len(currentChainState))
	for token, state := range currentChainState {
		state.Sticks = state.StickList()
		xcList = append(xcList, &TokenedExchange{
			Token: token,
			State: state,
		})
	}
	sort.Slice(xcList, func(i, j int) bool {
		if xcList[i].Token == "binance" {
			return true
		}
		if xcList[j].Token == "binance" {
			return false
		}
		return xcList[i].State.Volume > xcList[j].State.Volume
	})
	return xcList
}

func (state *ExchangeBotState) GetMutilchainPrice(chainType string) float64 {
	switch chainType {
	case TYPEBTC:
		return state.BTCPrice
	case TYPELTC:
		return state.LTCPrice
	default:
		return state.Price
	}
}

func (state *ExchangeBotState) GetMutilchainLowHighPrice(chainType string) (float64, float64) {
	switch chainType {
	case TYPEBTC:
		return state.BTCLowPrice, state.BTCHighPrice
	case TYPELTC:
		return state.LTCLowPrice, state.LTCHighPrice
	default:
		return state.LowPrice, state.HighPrice
	}
}

func (state *ExchangeBotState) GetMutilchainPriceChange(chainType string) float64 {
	switch chainType {
	case TYPEBTC:
		return state.BTCPriceChange
	case TYPELTC:
		return state.LTCPriceChange
	default:
		return state.DCRUSD24hChange
	}
}

func (state *ExchangeBotState) GetMutilchainVolumn(chainType string) float64 {
	switch chainType {
	case TYPEBTC:
		return state.BTCVolume
	case TYPELTC:
		return state.LTCVolume
	default:
		return state.Volume
	}
}

func (state *ExchangeBotState) GetMutilchainExchangeState(chainType string) map[string]*ExchangeState {
	switch chainType {
	case TYPEBTC:
		return state.BtcUsd
	case TYPELTC:
		return state.LtcUsd
	default:
		return state.DcrBtc
	}
}

// A price bin for the aggregated orderbook. The Volumes array will be length
// N = number of depth-reporting exchanges. If any exchange has an order book
// entry at price Price, then an agBookPt should be created. If a different
// exchange does not have an order at Price, there will be a 0 in Volumes at
// the exchange's index. An exchange's index in Volumes is set by its index
// in (aggregateOrderbook).Tokens.
type agBookPt struct {
	Price   float64   `json:"price"`
	Volumes []float64 `json:"volumes"`
}

// The aggregated depth data. Similar to DepthData, but with agBookPts instead.
// For aggregateData, the Time will indicate the most recent time at which an
// exchange with non-nil DepthData was updated.
type aggregateData struct {
	Time int64      `json:"time"`
	Bids []agBookPt `json:"bids"`
	Asks []agBookPt `json:"asks"`
}

// An aggregated orderbook. Combines all data from the DepthData of each
// Exchange. For aggregatedOrderbook, the Expiration is set to the time of the
// most recent DepthData update plus an additional (ExchangeBot).RequestExpiry,
// though new data may be available before then.
type aggregateOrderbook struct {
	BtcIndex    string        `json:"btc_index"`
	Price       float64       `json:"price"`
	Tokens      []string      `json:"tokens"`
	UpdateTimes []int64       `json:"update_times"`
	Data        aggregateData `json:"data"`
	Expiration  int64         `json:"expiration"`
}

// FiatIndices maps currency codes to Bitcoin exchange rates.
type FiatIndices map[string]float64

// IndexUpdate is sent from the Exchange to the ExchangeBot indexChan when new
// data is received.
type IndexUpdate struct {
	Token   string
	Indices FiatIndices
}

// BotChannels is passed to exchanges for communication with the Start loop.
type BotChannels struct {
	index       chan *IndexUpdate
	exchange    chan *ExchangeUpdate
	ltcExchange chan *ExchangeUpdate
	btcExchange chan *ExchangeUpdate
	done        chan struct{}
}

// UpdateChannels are requested by the user with ExchangeBot.UpdateChannels.
type UpdateChannels struct {
	Exchange chan *ExchangeUpdate
	Index    chan *IndexUpdate
	Quit     chan struct{}
}

// The chart data structures that are encoded and cached are the
// candlestickResponse and the depthResponse.
type candlestickResponse struct {
	BtcIndex   string       `json:"index"`
	Price      float64      `json:"price"`
	Sticks     Candlesticks `json:"sticks"`
	Expiration int64        `json:"expiration"`
}

type depthResponse struct {
	BtcIndex   string     `json:"index"`
	Price      float64    `json:"price"`
	Data       *DepthData `json:"data"`
	Expiration int64      `json:"expiration"`
}

// versionedChart holds a pre-encoded byte slice of a chart's data along with a
// version number that can be compared for use in caching.
type versionedChart struct {
	chartID string
	dataID  int
	chart   []byte
}

func genCacheID(parts ...string) string {
	return strings.Join(parts, "-")
}

func genMutilchainCacheID(chainType string, parts ...string) string {
	return fmt.Sprintf("%s-%s", chainType, strings.Join(parts, "-"))
}

// NewExchangeBot constructs a new ExchangeBot with the provided configuration.
func NewExchangeBot(config *ExchangeBotConfig) (*ExchangeBot, error) {
	// Validate configuration
	if config.DataExpiry == "" {
		config.DataExpiry = DefaultDataExpiry
	}
	if config.RequestExpiry == "" {
		config.RequestExpiry = DefaultRequestExpiry
	}
	dataExpiry, err := time.ParseDuration(config.DataExpiry)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse data expiration from %s", config.DataExpiry)
	}
	requestExpiry, err := time.ParseDuration(config.RequestExpiry)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse request expiration from %s", config.RequestExpiry)
	}
	if requestExpiry < dataExpiry {
		return nil, fmt.Errorf("Request expiration must be longer than data expiration")
	}
	if dataExpiry < time.Minute {
		return nil, fmt.Errorf("Expiration must be at least one minute")
	}
	if config.BtcIndex == "" {
		config.BtcIndex = DefaultCurrency
	}

	bot := &ExchangeBot{
		DcrBtcExchanges: make(map[string]Exchange),
		LTCUSDExchanges: make(map[string]Exchange),
		BTCUSDExchanges: make(map[string]Exchange),
		IndexExchanges:  make(map[string]Exchange),
		Exchanges:       make(map[string]Exchange),
		LTCExchanges:    make(map[string]Exchange),
		BTCExchanges:    make(map[string]Exchange),
		versionedCharts: make(map[string]*versionedChart),
		chartVersions:   make(map[string]int),
		BtcIndex:        config.BtcIndex,
		indexMap:        make(map[string]FiatIndices),
		currentState: ExchangeBotState{
			BtcIndex:    config.BtcIndex,
			Price:       0,
			LTCPrice:    0,
			BTCPrice:    0,
			Volume:      0,
			LTCVolume:   0,
			BTCVolume:   0,
			DcrBtc:      make(map[string]*ExchangeState),
			LtcUsd:      make(map[string]*ExchangeState),
			BtcUsd:      make(map[string]*ExchangeState),
			FiatIndices: make(map[string]*ExchangeState),
		},
		currentStateBytes: []byte{},
		DataExpiry:        dataExpiry,
		RequestExpiry:     requestExpiry,
		minTick:           5 * time.Second,
		updateChans:       []chan *ExchangeUpdate{},
		indexChans:        []chan *IndexUpdate{},
		quitChans:         []chan struct{}{},
		exchangeChan:      make(chan *ExchangeUpdate, 16),
		indexChan:         make(chan *IndexUpdate, 16),
		client:            new(http.Client),
		config:            config,
		failed:            false,
	}

	if config.MasterBot != "" {
		if config.MasterCertFile == "" {
			return nil, fmt.Errorf("No TLS certificate path provided")
		}
		bot.TLSCredentials, err = credentials.NewClientTLSFromFile(config.MasterCertFile, "")
		if err != nil {
			return nil, fmt.Errorf("Failed to load TLS certificate: %v", err)
		}
		host, port, err := net.SplitHostPort(config.MasterBot)
		if err != nil {
			if !strings.Contains(err.Error(), "missing port in address") {
				return nil, fmt.Errorf("Unable to parse master bot address %s: %v", config.MasterBot, err)
			}
			port = defaultDCRRatesPort
		}
		if host == "" {
			// For addresses passed in form :[port], quietly substitute localhost. A
			// hostname is required for the gRPC TLS connection.
			host = "localhost"
		}
		config.MasterBot = host + ":" + port
	}

	isDisabled := func(token string) bool {
		for _, tkn := range config.Disabled {
			if tkn == token {
				return true
			}
		}
		return false
	}

	quit := make(chan struct{})
	bot.quitChans = append(bot.quitChans, quit)

	channels := &BotChannels{
		index:    bot.indexChan,
		exchange: bot.exchangeChan,
		done:     quit,
	}

	buildExchange := func(token string, constructor func(*http.Client, *BotChannels, string) (Exchange, error), xcMap map[string]Exchange, binanceApiUrl string) {
		if isDisabled(token) {
			return
		}
		if constructor == nil {
			return
		}
		xc, err := constructor(bot.client, channels, binanceApiUrl)
		if err != nil {
			return
		}
		xcMap[token] = xc
		bot.Exchanges[token] = xc
	}

	SetMutilchainExchanges := func(token string, chainType string, xc Exchange) {
		switch chainType {
		case TYPELTC:
			bot.LTCExchanges[token] = xc
		case TYPEBTC:
			bot.BTCExchanges[token] = xc
		default:
			return
		}
	}

	buildMutilchainExchange := func(token string, constructor func(*http.Client, *BotChannels, string, string) (Exchange, error), xcMap map[string]Exchange, chainType, binanceApiUrl string) {
		if isDisabled(token) {
			return
		}
		if constructor == nil {
			return
		}
		xc, err := constructor(bot.client, channels, chainType, binanceApiUrl)
		if err != nil {
			return
		}
		xcMap[token] = xc
		SetMutilchainExchanges(token, chainType, xc)
	}

	for token, constructor := range BtcIndices {
		buildExchange(token, constructor, bot.IndexExchanges, config.BinanceAPIURL)
	}

	for token, constructor := range DcrExchanges {
		buildExchange(token, constructor, bot.DcrBtcExchanges, config.BinanceAPIURL)
	}

	for token, constructor := range LTCExchanges {
		buildMutilchainExchange(token, constructor, bot.LTCUSDExchanges, TYPELTC, config.BinanceAPIURL)
	}

	for token, constructor := range BTCExchanges {
		buildMutilchainExchange(token, constructor, bot.BTCUSDExchanges, TYPEBTC, config.BinanceAPIURL)
	}

	if len(bot.DcrBtcExchanges) == 0 {
		return nil, fmt.Errorf("no DCR-BTC exchanges were initialized")
	}

	if len(bot.BTCUSDExchanges) == 0 {
		return nil, fmt.Errorf("no BTC-USD exchanges were initialized")
	}

	if len(bot.LTCUSDExchanges) == 0 {
		return nil, fmt.Errorf("no LTC-USD exchanges were initialized")
	}

	if len(bot.IndexExchanges) == 0 {
		return nil, fmt.Errorf("no BTC-fiat exchanges were initialized")
	}

	return bot, nil
}

// Start is the main ExchangeBot loop, reading from the exchange update channel
// and scheduling refresh cycles.
func (bot *ExchangeBot) Start(ctx context.Context, wg *sync.WaitGroup) {
	tick := time.NewTimer(time.Second)
	config := bot.config
	reconnectionAttempt := 0
	if config.MasterBot != "" {
		stream, err := bot.connectMasterBot(ctx, 0)
		if err != nil {
			log.Errorf("Failed to initialize gRPC stream. Falling back to direct connection: %v", err)
		} else {
			// Drain the timer to prevent the first cycle
			if !tick.Stop() {
				<-tick.C
			}
			// Start a loop to listen for updates from the dcrrates server.
			go func() {
				for {
					update, err := stream.Recv()
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						log.Errorf("DCRRates error. Attempting to reconnect in 10 seconds: %v", err)
						delay := 10 * time.Second
						delayString := "10 seconds"
						// Try to reconnect every minute until a connection is made.
						for {
							reconnectionAttempt++
							stream, err = bot.connectMasterBot(ctx, delay)
							if err == nil {
								break
							} else {
								if ctx.Err() != nil {
									return
								}
								if reconnectionAttempt > 12 { // ~ two minutes
									delay = time.Minute
									delayString = "1 minute"
								}
								log.Errorf("Failed to reconnect to DCRRates. Attempting to reconnect in %s: %v", delayString, err)
							}
						}
						log.Infof("DCRRates connection re-established.")
						reconnectionAttempt = 0
						continue
					}
					// Send the update through the Exchange so that appropriate attributes
					// are set.
					if IsDcrExchange(update.Token, update.Symbol) {
						state := exchangeStateFromProto(update)
						bot.Exchanges[update.Token].Update(state)
					} else if IsBtcIndex(update.Token) {
						bot.Exchanges[update.Token].UpdateIndices(update.GetIndices())
					}
					if IsLTCExchange(update.Token, update.Symbol) {
						state := exchangeStateFromProto(update)
						bot.LTCExchanges[update.Token].Update(state)
					}
					if IsBTCExchange(update.Token, update.Symbol) {
						state := exchangeStateFromProto(update)
						bot.BTCExchanges[update.Token].Update(state)
					}
				}
			}()
		}
	} // End DCRRates master
	if bot.masterConnection == nil {
		// Start refresh on all exchanges, and then change the updateTimes to
		// de-sync the updates.
		timeBetween := bot.DataExpiry / time.Duration(len(bot.Exchanges))
		idx := 0
		ltcIdx := 0
		btcIdx := 0
		for _, xc := range bot.Exchanges {
			go func(xc Exchange, d int) {
				xc.Refresh()
				if !xc.IsFailed() {
					xc.Hurry(timeBetween * time.Duration(d))
				}
			}(xc, idx)
			idx++
		}

		for _, xc := range bot.LTCExchanges {
			go func(xc Exchange, d int) {
				xc.Refresh()
				if !xc.IsFailed() {
					xc.Hurry(timeBetween * time.Duration(d))
				}
			}(xc, ltcIdx)
			ltcIdx++
		}
		for _, xc := range bot.BTCExchanges {
			go func(xc Exchange, d int) {
				xc.Refresh()
				if !xc.IsFailed() {
					xc.Hurry(timeBetween * time.Duration(d))
				}
			}(xc, btcIdx)
			btcIdx++
		}
	}

out:
	for {
		select {
		case update := <-bot.exchangeChan:
			log.Tracef("exchange update received from %s with a BTC price %f, ", update.Token, update.State.Price)
			err := bot.updateExchange(update)
			if err != nil {
				log.Warnf("Error encountered in exchange update: %v", err)
				continue
			}
			bot.signalExchangeUpdate(update)
		case update := <-bot.indexChan:
			btcPrice, found := update.Indices[bot.BtcIndex]
			if found {
				log.Tracef("index update received from %s with %d indices, %s price for Bitcoin is %f", update.Token, len(update.Indices), bot.BtcIndex, btcPrice)
			}
			err := bot.updateIndices(update)
			if err != nil {
				log.Warnf("Error encountered in index update: %v", err)
				continue
			}
			bot.signalIndexUpdate(update)
		case <-tick.C:
			bot.Cycle()
		case <-ctx.Done():
			break out
		}
		if bot.masterConnection == nil {
			tick = bot.nextTick()
		}
	}
	if wg != nil {
		wg.Done()
	}
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	for _, ch := range bot.quitChans {
		close(ch)
	}
	if bot.masterConnection != nil {
		bot.masterConnection.Close()
	}
}

// Attempt DCRRates connection after delay.
func (bot *ExchangeBot) connectMasterBot(ctx context.Context, delay time.Duration) (dcrrates.DCRRates_SubscribeExchangesClient, error) {
	if bot.masterConnection != nil {
		bot.masterConnection.Close()
	}
	if delay > 0 {
		expiration := time.NewTimer(delay)
		select {
		case <-expiration.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("Context cancelled before reconnection")
		}
	}
	conn, err := grpc.Dial(bot.config.MasterBot, grpc.WithTransportCredentials(bot.TLSCredentials))
	if err != nil {
		log.Warnf("gRPC connection error when trying to connect to %s. Falling back to direct connection: %v", bot.config.MasterBot, err)
		return nil, err
	}
	bot.masterConnection = conn
	grpcClient := dcrrates.NewDCRRatesClient(conn)
	stream, err := grpcClient.SubscribeExchanges(ctx, &dcrrates.ExchangeSubscription{
		BtcIndex:     bot.BtcIndex,
		Exchanges:    bot.subscribedExchanges(),
		LtcExchanges: bot.subscribedMutilchainExchanges(TYPELTC),
		BtcExchanges: bot.subscribedMutilchainExchanges(TYPEBTC),
	})
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (bot *ExchangeBot) getMutilchainExchanges(chainType string) map[string]Exchange {
	switch chainType {
	case TYPEBTC:
		return bot.BTCExchanges
	case TYPELTC:
		return bot.LTCExchanges
	default:
		return make(map[string]Exchange)
	}
}

// A list of exchanges which the ExchangeBot is monitoring.
func (bot *ExchangeBot) subscribedMutilchainExchanges(chainType string) []string {
	exchanges := bot.getMutilchainExchanges(chainType)
	xcList := make([]string, 0, len(exchanges))
	for token := range exchanges {
		xcList = append(xcList, token)
	}
	return xcList
}

// A list of exchanges which the ExchangeBot is monitoring.
func (bot *ExchangeBot) subscribedExchanges() []string {
	xcList := make([]string, 0, len(bot.Exchanges))
	for token := range bot.Exchanges {
		xcList = append(xcList, token)
	}
	return xcList
}

// UpdateChannels creates an UpdateChannels, which holds a channel to receive
// exchange updates and a channel which is closed when the start loop exits.
func (bot *ExchangeBot) UpdateChannels() *UpdateChannels {
	update := make(chan *ExchangeUpdate, 16)
	index := make(chan *IndexUpdate, 16)
	quit := make(chan struct{})
	bot.mtx.Lock()
	defer bot.mtx.Unlock()
	bot.updateChans = append(bot.updateChans, update)
	bot.indexChans = append(bot.indexChans, index)
	bot.quitChans = append(bot.quitChans, quit)
	return &UpdateChannels{
		Exchange: update,
		Index:    index,
		Quit:     quit,
	}
}

// Send an update to any channels requested with bot.UpdateChannels().
func (bot *ExchangeBot) signalExchangeUpdate(update *ExchangeUpdate) {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	for _, ch := range bot.updateChans {
		select {
		case ch <- update:
		default:
			log.Warnf("Failed to write update to exchange update channel")
		}
	}
}

func (bot *ExchangeBot) signalIndexUpdate(update *IndexUpdate) {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	for _, ch := range bot.indexChans {
		select {
		case ch <- update:
		default:
		}
	}
}

// State is a copy of the current ExchangeBotState. A JSON-encoded byte array
// of the current state can be accessed through StateBytes().
func (bot *ExchangeBot) State() *ExchangeBotState {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	return bot.stateCopy
}

// ConvertedState returns an ExchangeBotState with a base of the provided
// currency code, if available.
func (bot *ExchangeBot) ConvertedState(code string) (*ExchangeBotState, error) {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	fiatIndices := make(map[string]*ExchangeState)
	for token, indices := range bot.indexMap {
		for symbol, price := range indices {
			if symbol == code {
				fiatIndices[token] = &ExchangeState{BaseState: BaseState{Price: price}}
			}
		}
	}

	dcrPrice, dcrChange, volume, low, high := bot.processState(bot.currentState.DcrBtc, true)
	dcrBtcPrice, dcrBtcChange, dcrBtcvolume := bot.processDCRBTCState(bot.currentState.DcrBtc, true)
	ltcPrice, ltcChange, ltcVolumn, ltcLow, ltcHigh := bot.processState(bot.currentState.LtcUsd, true)
	btcExchangePrice, btcUsdChange, btcVolumn, btcLow, btcHigh := bot.processState(bot.currentState.BtcUsd, true)
	btcPrice, _, _, _, _ := bot.processState(fiatIndices, false)
	if dcrPrice == 0 || btcPrice == 0 {
		bot.failed = true
		return nil, fmt.Errorf("Unable to process price for currency %s", code)
	}
	state := ExchangeBotState{
		BtcIndex:          code,
		Volume:            volume * btcPrice,
		Price:             dcrPrice,
		LowPrice:          low,
		HighPrice:         high,
		LTCPrice:          ltcPrice,
		LTCLowPrice:       ltcLow,
		LTCHighPrice:      ltcHigh,
		LTCVolume:         ltcVolumn * ltcPrice,
		BTCPrice:          btcExchangePrice,
		BTCLowPrice:       btcLow,
		BTCHighPrice:      btcHigh,
		BTCVolume:         btcVolumn * btcExchangePrice,
		BtcPrice:          btcPrice,
		DcrBtc:            bot.currentState.DcrBtc,
		LtcUsd:            bot.currentState.LtcUsd,
		BtcUsd:            bot.currentState.BtcUsd,
		BTCUSDPriceChange: btcUsdChange,
		FiatIndices:       fiatIndices,
		DCRBTCPrice:       dcrBtcPrice,
		DCRBTCVolume:      dcrBtcvolume,
		DCRUSD24hChange:   dcrChange,
		DCRBTC24hChange:   dcrBtcChange,
		LTCPriceChange:    ltcChange,
	}

	return state.copy(), nil
}

// ExchangeRates is the dcr and btc prices converted to fiat.
type ExchangeRates struct {
	BtcIndex  string               `json:"btcIndex"`
	DcrPrice  float64              `json:"dcrPrice"`
	BtcPrice  float64              `json:"btcPrice"`
	Exchanges map[string]BaseState `json:"exchanges"`
}

// Rates is the current exchange rates for dcr and btc.
func (bot *ExchangeBot) Rates() *ExchangeRates {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	s := bot.stateCopy

	xcs := make(map[string]BaseState, len(s.DcrBtc))
	for token, xcState := range s.DcrBtc {
		xcs[token] = xcState.BaseState
	}

	return &ExchangeRates{
		BtcIndex:  s.BtcIndex,
		DcrPrice:  s.Price,
		BtcPrice:  s.BtcPrice,
		Exchanges: xcs,
	}
}

// ConvertedRates returns an ExchangeRates with a base of the provided currency
// code, if available.
func (bot *ExchangeBot) ConvertedRates(code string) (*ExchangeRates, error) {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	fiatIndices := make(map[string]*ExchangeState)
	for token, indices := range bot.indexMap {
		for symbol, price := range indices {
			if symbol == code {
				fiatIndices[token] = &ExchangeState{BaseState: BaseState{Price: price}}
			}
		}
	}

	dcrPrice, _, _, _, _ := bot.processState(bot.currentState.DcrBtc, true)
	btcPrice, _, _, _, _ := bot.processState(fiatIndices, false)
	if dcrPrice == 0 || btcPrice == 0 {
		bot.failed = true
		return nil, fmt.Errorf("Unable to process price for currency %s", code)
	}
	return &ExchangeRates{
		BtcIndex: code,
		DcrPrice: dcrPrice,
		BtcPrice: btcPrice,
	}, nil
}

// StateBytes is a JSON-encoded byte array of the currentState.
func (bot *ExchangeBot) StateBytes() []byte {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	return bot.currentStateBytes
}

// Encodes the thing as JSON, with indentation if configured.
func (bot *ExchangeBot) encodeJSON(thing interface{}) ([]byte, error) {
	if bot.config.Indent {
		return json.MarshalIndent(thing, "", "    ")
	}
	return json.Marshal(thing)
}

// ConvertedStateBytes gives a JSON-encoded byte array of the currentState
// with a base of the provided currency code, if available.
func (bot *ExchangeBot) ConvertedStateBytes(symbol string) ([]byte, error) {
	state, err := bot.ConvertedState(symbol)
	if err != nil {
		return nil, err
	}
	return bot.encodeJSON(state)
}

// AvailableIndices creates a fresh slice of all available index currency codes.
func (bot *ExchangeBot) AvailableIndices() []string {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	var indices sort.StringSlice
	add := func(index string) {
		for _, symbol := range indices {
			if symbol == index {
				return
			}
		}
		indices = append(indices, index)
	}
	for _, fiatIndices := range bot.indexMap {
		for symbol := range fiatIndices {
			add(symbol)
		}
	}
	sort.Sort(indices)
	return indices
}

// Indices is the fiat indices for a given BTC index exchange.
func (bot *ExchangeBot) Indices(token string) FiatIndices {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	indices := make(FiatIndices)
	for code, price := range bot.indexMap[token] {
		indices[code] = price
	}
	return indices
}

func (bot *ExchangeBot) incrementChart(chartId string) {
	_, found := bot.chartVersions[chartId]
	if found {
		bot.chartVersions[chartId]++
	} else {
		bot.chartVersions[chartId] = 0
	}
}

func (bot *ExchangeBot) cachedChartVersion(chartId string) int {
	cid, found := bot.chartVersions[chartId]
	if !found {
		return -1
	}
	return cid
}

func (bot *ExchangeBot) processMutilchainState(states map[string]*ExchangeState, exchanges map[string]Exchange, volumeAveraged bool) (float64, float64, float64, float64, float64) {
	var priceAccumulator, volSum, changeSum float64
	var deletions []string
	oldestValid := time.Now().Add(-bot.RequestExpiry)
	lowPrice := math.MaxFloat64
	highPrice := float64(0)
	for token, state := range states {
		if exchanges[token].LastUpdate().Before(oldestValid) {
			deletions = append(deletions, token)
			continue
		}
		volume := 1.0
		if volumeAveraged {
			volume = state.BaseVolume
		}
		volSum += volume
		priceAccumulator += volume * state.Price
		changeSum += volume * state.Change
		// compare with low price
		if state.Low > 0 && state.Low < lowPrice {
			lowPrice = state.Low
		}
		// compare with high price
		if state.High > 0 && state.High > highPrice {
			highPrice = state.High
		}
	}
	for _, token := range deletions {
		delete(states, token)
	}
	if volSum == 0 {
		return 0, 0, 0, 0, 0
	}
	return priceAccumulator / volSum, changeSum / volSum, volSum, lowPrice, highPrice
}

// processState is a helper function to process a slice of ExchangeState into
// a price, and optionally a volume sum, and perform some cleanup along the way.
// If volumeAveraged is false, all exchanges are given equal weight in the avg.
func (bot *ExchangeBot) processState(states map[string]*ExchangeState, volumeAveraged bool) (float64, float64, float64, float64, float64) {
	var priceAccumulator, volSum, changeSum float64
	var deletions []string
	oldestValid := time.Now().Add(-bot.RequestExpiry)
	lowPrice := math.MaxFloat64
	highPrice := float64(0)
	for token, state := range states {
		if IsDCRBTCExchange(token) {
			continue
		}
		if bot.Exchanges[token].LastUpdate().Before(oldestValid) {
			deletions = append(deletions, token)
			continue
		}
		volume := 1.0
		if volumeAveraged {
			volume = state.BaseVolume
		}
		volSum += volume
		priceAccumulator += volume * state.Price
		changeSum += volume * state.Change
		// compare with low price
		if state.Low > 0 && state.Low < lowPrice {
			lowPrice = state.Low
		}
		// compare with high price
		if state.High > 0 && state.High > highPrice {
			highPrice = state.High
		}
	}
	for _, token := range deletions {
		delete(states, token)
	}
	if volSum == 0 {
		return 0, 0, 0, 0, 0
	}
	if lowPrice == math.MaxFloat64 {
		lowPrice = 0
	}
	return priceAccumulator / volSum, changeSum / volSum, volSum, lowPrice, highPrice
}

func (bot *ExchangeBot) processDCRBTCState(states map[string]*ExchangeState, volumeAveraged bool) (float64, float64, float64) {
	var priceAccumulator, volSum, changeSum float64
	var deletions []string
	oldestValid := time.Now().Add(-bot.RequestExpiry)
	for token, state := range states {
		if !IsDCRBTCExchange(token) {
			continue
		}
		if bot.Exchanges[token].LastUpdate().Before(oldestValid) {
			deletions = append(deletions, token)
			continue
		}
		volume := 1.0
		if volumeAveraged {
			volume = state.BaseVolume
		}
		volSum += volume
		priceAccumulator += volume * state.Price
		changeSum += volume * state.Change
	}
	for _, token := range deletions {
		delete(states, token)
	}
	if volSum == 0 {
		return 0, 0, 0
	}
	return priceAccumulator / volSum, changeSum / volSum, volSum
}

// updateExchange processes an update from a Decred-BTC Exchange.
func (bot *ExchangeBot) updateExchange(update *ExchangeUpdate) error {
	bot.mtx.Lock()
	defer bot.mtx.Unlock()
	if update.State.Candlesticks != nil {
		for bin := range update.State.Candlesticks {
			bot.incrementChart(genCacheID(update.Token, string(bin)))
		}
	}
	if update.State.Depth != nil {
		bot.incrementChart(genCacheID(update.Token, orderbookKey))
		bot.incrementChart(genCacheID(aggregatedOrderbookKey, orderbookKey))
		bot.incrementChart(genCacheID(aggregatedBTCOrderbookKey, orderbookKey))
	}
	var chainType string
	switch update.State.Symbol {
	case LTCSYMBOL:
		bot.currentState.LtcUsd[update.Token] = update.State
		chainType = TYPELTC
	case BTCSYMBOL:
		bot.currentState.BtcUsd[update.Token] = update.State
		chainType = TYPEBTC
	default:
		bot.currentState.DcrBtc[update.Token] = update.State
		chainType = TYPEDCR
	}
	return bot.updateMutilchainState(chainType)
}

// updateIndices processes an update from an Bitcoin index source, essentially
// a map pairing currency codes to bitcoin prices.
func (bot *ExchangeBot) updateIndices(update *IndexUpdate) error {
	bot.mtx.Lock()
	defer bot.mtx.Unlock()
	bot.indexMap[update.Token] = update.Indices
	price, hasCode := update.Indices[bot.config.BtcIndex]
	if hasCode {
		bot.currentState.FiatIndices[update.Token] = &ExchangeState{
			BaseState: BaseState{
				Price: price,
				Stamp: time.Now().Unix(),
			},
		}
		return bot.updateState()
	}
	log.Warnf("Default currency code, %s, not contained in update from %s", bot.BtcIndex, update.Token)
	return nil
}

func (bot *ExchangeBot) updateMutilchainState(chainType string) error {
	switch chainType {
	case TYPELTC:
		ltcPrice, ltcChange, ltcVolumn, ltcLow, ltcHigh := bot.processMutilchainState(bot.currentState.LtcUsd, bot.LTCExchanges, true)
		if ltcPrice == 0 {
			bot.failed = true
		} else {
			bot.failed = false
			bot.currentState.LTCPrice = ltcPrice
			bot.currentState.LTCVolume = ltcVolumn
			bot.currentState.LTCLowPrice = ltcLow
			bot.currentState.LTCHighPrice = ltcHigh
			bot.currentState.LTCPriceChange = ltcChange
		}
	case TYPEBTC:
		btcPrice, btcChange, btcVolumn, btcLow, btcHigh := bot.processMutilchainState(bot.currentState.BtcUsd, bot.BTCExchanges, true)
		if btcPrice == 0 {
			bot.failed = true
		} else {
			bot.failed = false
			bot.currentState.BTCPrice = btcPrice
			bot.currentState.BTCVolume = btcVolumn
			bot.currentState.BTCLowPrice = btcLow
			bot.currentState.BTCHighPrice = btcHigh
			bot.currentState.BTCPriceChange = btcChange
		}
	default:
		dcrPrice, dcrChange, volume, lowPrice, highPrice := bot.processState(bot.currentState.DcrBtc, true)
		dcrBtcPrice, dcrBtcChange, dcrBtcVolume := bot.processDCRBTCState(bot.currentState.DcrBtc, true)
		btcPrice, _, _, _, _ := bot.processState(bot.currentState.FiatIndices, false)
		if dcrPrice == 0 || btcPrice == 0 {
			bot.failed = true
		} else {
			bot.failed = false
			bot.currentState.Price = dcrPrice
			bot.currentState.BtcPrice = btcPrice
			bot.currentState.Volume = volume
			bot.currentState.DCRBTCPrice = dcrBtcPrice
			bot.currentState.DCRBTCVolume = dcrBtcVolume
			bot.currentState.LowPrice = lowPrice
			bot.currentState.HighPrice = highPrice
			bot.currentState.DCRUSD24hChange = dcrChange
			bot.currentState.DCRBTC24hChange = dcrBtcChange
		}
	}

	var jsonBytes []byte
	var err error
	if bot.config.Indent {
		jsonBytes, err = json.MarshalIndent(bot.currentState, "", "    ")
	} else {
		jsonBytes, err = json.Marshal(bot.currentState)
	}
	if err != nil {
		return fmt.Errorf("Failed to write bytes: %v", err)
	}
	bot.currentStateBytes = jsonBytes
	bot.stateCopy = bot.currentState.copy()

	return nil
}

// Called from both updateIndices and updateExchange (under mutex lock).
func (bot *ExchangeBot) updateState() error {
	dcrPrice, dcrChange, volume, lowPrice, highPrice := bot.processState(bot.currentState.DcrBtc, true)
	dcrBtcPrice, dcrBtcChange, dcrBtcVolume := bot.processDCRBTCState(bot.currentState.DcrBtc, true)
	btcPrice, _, _, _, _ := bot.processState(bot.currentState.FiatIndices, false)
	if dcrPrice == 0 || btcPrice == 0 {
		bot.failed = true
	} else {
		bot.failed = false
		bot.currentState.Price = dcrPrice
		bot.currentState.BtcPrice = btcPrice
		bot.currentState.Volume = volume
		bot.currentState.LowPrice = lowPrice
		bot.currentState.HighPrice = highPrice
		bot.currentState.DCRUSD24hChange = dcrChange
		bot.currentState.DCRBTCPrice = dcrBtcPrice
		bot.currentState.DCRBTC24hChange = dcrBtcChange
		bot.currentState.DCRBTCVolume = dcrBtcVolume
	}

	var jsonBytes []byte
	var err error
	if bot.config.Indent {
		jsonBytes, err = json.MarshalIndent(bot.currentState, "", "    ")
	} else {
		jsonBytes, err = json.Marshal(bot.currentState)
	}
	if err != nil {
		return fmt.Errorf("Failed to write bytes: %v", err)
	}
	bot.currentStateBytes = jsonBytes
	bot.stateCopy = bot.currentState.copy()

	return nil
}

// IsFailed is whether the failed flag was set during the last IndexUpdate
// or ExchangeUpdate. The failed flag is set when either no Bitcoin Index
// sources or no Decred Exchanges are up-to-date. Individual exchanges can
// be outdated/failed without IsFailed being false, as long as there is at least
// one Bitcoin index and one Decred exchange.
func (bot *ExchangeBot) IsFailed() bool {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	return bot.failed
}

// nextTick checks the exchanges' last update and fail times, and calculates
// when the next Cycle should run.
func (bot *ExchangeBot) nextTick() *time.Timer {
	tNow := time.Now()
	tOldest := tNow
	for _, xc := range bot.Exchanges {
		t := xc.LastTry()
		if t.Before(tOldest) {
			tOldest = t
		}
	}
	tSince := tNow.Sub(tOldest)
	tilNext := bot.DataExpiry - tSince
	if tilNext < bot.minTick {
		tilNext = bot.minTick
	}
	return time.NewTimer(tilNext)
}

// Cycle refreshes all expired exchanges.
func (bot *ExchangeBot) Cycle() {
	tNow := time.Now()
	for _, xc := range bot.Exchanges {
		if tNow.Sub(xc.LastTry()) > bot.DataExpiry {
			go xc.Refresh()
		}
	}
	for _, xc := range bot.LTCExchanges {
		if tNow.Sub(xc.LastTry()) > bot.DataExpiry {
			go xc.Refresh()
		}
	}
	for _, xc := range bot.BTCExchanges {
		if tNow.Sub(xc.LastTry()) > bot.DataExpiry {
			go xc.Refresh()
		}
	}
}

// Price gets the lastest Price in the default currency (BtcIndex).
func (bot *ExchangeBot) Price() float64 {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	return bot.currentState.Price
}

// Price gets the lastest Price in the default currency (BtcIndex).
func (bot *ExchangeBot) MutilchainPrice(chainSymbol string) float64 {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	switch chainSymbol {
	case BTCSYMBOL:
		return bot.currentState.BTCPrice
	case LTCSYMBOL:
		return bot.currentState.LTCPrice
	default:
		return bot.currentState.Price
	}
}

// Conversion is a representation of some amount of DCR in another index.
type Conversion struct {
	Value float64 `json:"value"`
	Index string  `json:"index"`
}

// TwoDecimals is a string representation of the value with two digits after
// the decimal point, but will show more to achieve at least three significant
// digits.
func (c *Conversion) TwoDecimals() string {
	if c.Value == 0.0 {
		return "0.00"
	} else if c.Value < 1.0 && c.Value > -1.0 {
		return fmt.Sprintf("%3g", c.Value)
	}
	return fmt.Sprintf("%.2f", c.Value)
}

// Conversion attempts to multiply the supplied float with the default index.
// Nil pointer will be returned if there is no valid exchangeState.
func (bot *ExchangeBot) Conversion(dcrVal float64) *Conversion {
	if bot == nil {
		return nil
	}
	xcState := bot.State()
	if xcState != nil {
		return &Conversion{
			Value: xcState.Price * dcrVal,
			Index: xcState.BtcIndex,
		}
	}
	// Haven't gotten data yet, but we're running.
	return &Conversion{Value: 0, Index: DefaultCurrency}
}

func (bot *ExchangeBot) MutilchainConversion(coinVal float64, chainType string) *Conversion {
	if bot == nil {
		return nil
	}
	xcState := bot.State()
	if xcState != nil {
		return &Conversion{
			Value: xcState.GetMutilchainPrice(chainType) * coinVal,
			Index: xcState.BtcIndex,
		}
	}
	// Haven't gotten data yet, but we're running.
	return &Conversion{Value: 0, Index: DefaultCurrency}
}

// Fetch the pre-encoded JSON chart data from the cache, if it exists and is not
// expired. Data is considered expired only when newer data has been received,
// but not necessarily requested/encoded yet. Boolean `hit` indicates success.
func (bot *ExchangeBot) fetchFromCache(chartID string) (data []byte, bestVersion int, hit bool) {
	bot.mtx.RLock()
	defer bot.mtx.RUnlock()
	bestVersion = bot.cachedChartVersion(chartID)
	cache, found := bot.versionedCharts[chartID]
	if found && cache.dataID == bestVersion {
		data = cache.chart
		hit = true
	}
	return
}

func (bot *ExchangeBot) MutilchainQuickSticks(token string, rawBin string, chainType string) ([]byte, error) {
	chartID := genMutilchainCacheID(chainType, token, rawBin)
	bin := candlestickKey(rawBin)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}
	// No hit on cache. Re-encode.
	bot.mtx.Lock()
	defer bot.mtx.Unlock()
	currentState := bot.currentState.GetMutilchainExchangeState(chainType)
	state, found := currentState[token]
	if !found {
		return nil, fmt.Errorf("Failed to find DCR exchange state for %s", token)
	}
	if state.Candlesticks == nil {
		return nil, fmt.Errorf("Failed to find candlesticks for %s", token)
	}

	sticks, found := state.Candlesticks[bin]
	if !found {
		return nil, fmt.Errorf("Failed to find candlesticks for %s and bin %s", token, rawBin)
	}
	if len(sticks) == 0 {
		return nil, fmt.Errorf("Empty candlesticks for %s and bin %s", token, rawBin)
	}

	expiration := sticks[len(sticks)-1].Start.Add(2 * bin.duration())

	chart, err := bot.encodeJSON(&candlestickResponse{
		BtcIndex:   bot.BtcIndex,
		Price:      bot.currentState.GetMutilchainPrice(chainType),
		Sticks:     sticks,
		Expiration: expiration.Unix(),
	})

	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s and bin %s", token, rawBin)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}

// QuickSticks returns the up-to-date candlestick data for the specified
// exchange and bin width, pulling from the cache if appropriate.
func (bot *ExchangeBot) QuickSticks(token string, rawBin string) ([]byte, error) {
	chartID := genCacheID(token, rawBin)
	bin := candlestickKey(rawBin)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}

	// No hit on cache. Re-encode.

	bot.mtx.Lock()
	defer bot.mtx.Unlock()
	state, found := bot.currentState.DcrBtc[token]
	if !found {
		return nil, fmt.Errorf("Failed to find DCR exchange state for %s", token)
	}
	if state.Candlesticks == nil {
		return nil, fmt.Errorf("Failed to find candlesticks for %s", token)
	}

	sticks, found := state.Candlesticks[bin]
	if !found {
		return nil, fmt.Errorf("Failed to find candlesticks for %s and bin %s", token, rawBin)
	}
	if len(sticks) == 0 {
		return nil, fmt.Errorf("Empty candlesticks for %s and bin %s", token, rawBin)
	}

	expiration := sticks[len(sticks)-1].Start.Add(2 * bin.duration())

	chart, err := bot.encodeJSON(&candlestickResponse{
		BtcIndex:   bot.BtcIndex,
		Price:      bot.currentState.Price,
		Sticks:     sticks,
		Expiration: expiration.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s and bin %s", token, rawBin)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}

// Move the DepthPoint array into a map whose entries are agBookPt, inserting
// the (DepthPoint).Quantity values at xcIndex of Volumes. Creates Volumes
// if it does not yet exist.
func mapifyDepthPoints(source []DepthPoint, target map[int64]agBookPt, xcIndex, ptCount int) {
	for _, pt := range source {
		k := eightPtKey(pt.Price)
		_, found := target[k]
		if !found {
			target[k] = agBookPt{
				Price:   pt.Price,
				Volumes: make([]float64, ptCount),
			}
		}
		target[k].Volumes[xcIndex] = pt.Quantity
	}
}

// A list of eightPtKey keys from an orderbook tracking map. Used for sorting.
func agBookMapKeys(book map[int64]agBookPt) []int64 {
	keys := make([]int64, 0, len(book))
	for k := range book {
		keys = append(keys, k)
	}
	return keys
}

// After the aggregate orderbook map is fully assembled, sort the keys and
// process the map into a list of lists.
func unmapAgOrders(book map[int64]agBookPt, reverse bool) []agBookPt {
	orderedBook := make([]agBookPt, 0, len(book))
	keys := agBookMapKeys(book)
	if reverse {
		sort.Slice(keys, func(i, j int) bool { return keys[j] < keys[i] })
	} else {
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	}
	for _, k := range keys {
		orderedBook = append(orderedBook, book[k])
	}
	return orderedBook
}

func (bot *ExchangeBot) aggMutilchainOrderbookSubMarketHandler(chainType string, tokenStr string) *aggregateOrderbook {
	state := bot.State()
	if state == nil {
		return nil
	}
	bids := make(map[int64]agBookPt)
	asks := make(map[int64]agBookPt)

	oldestUpdate := time.Now().Unix()
	var newestTime int64
	currentState := state.GetMutilchainExchangeState(chainType)
	// First, grab the tokens for exchanges with depth data so that they can be
	// counted and sorted alphabetically.
	tokenInputs := strings.Split(tokenStr, ",")
	tokens := []string{}
	for token, xcState := range currentState {
		if !xcState.HasDepth() || !slices.Contains(tokenInputs, token) {
			continue
		}
		tokens = append(tokens, token)
	}
	numXc := len(tokens)
	updateTimes := make([]int64, 0, numXc)
	sort.Strings(tokens)
	for i, token := range tokens {
		xcState := currentState[token]
		depth := xcState.Depth
		if depth.Time < oldestUpdate {
			oldestUpdate = depth.Time
		}
		if depth.Time > newestTime {
			newestTime = depth.Time
		}
		updateTimes = append(updateTimes, depth.Time)
		mapifyDepthPoints(depth.Bids, bids, i, numXc)
		mapifyDepthPoints(depth.Asks, asks, i, numXc)
	}
	return &aggregateOrderbook{
		Tokens:      tokens,
		BtcIndex:    bot.BtcIndex,
		Price:       state.GetMutilchainPrice(chainType),
		UpdateTimes: updateTimes,
		Data: aggregateData{
			Time: newestTime,
			Bids: unmapAgOrders(bids, true),
			Asks: unmapAgOrders(asks, false),
		},
		Expiration: oldestUpdate + int64(bot.RequestExpiry.Seconds()),
	}
}

func (bot *ExchangeBot) aggMutilchainOrderbook(chainType string) *aggregateOrderbook {
	state := bot.State()
	if state == nil {
		return nil
	}
	bids := make(map[int64]agBookPt)
	asks := make(map[int64]agBookPt)

	oldestUpdate := time.Now().Unix()
	var newestTime int64
	currentState := state.GetMutilchainExchangeState(chainType)
	// First, grab the tokens for exchanges with depth data so that they can be
	// counted and sorted alphabetically.
	tokens := []string{}
	for token, xcState := range currentState {
		if !xcState.HasDepth() {
			continue
		}
		tokens = append(tokens, token)
	}
	numXc := len(tokens)
	updateTimes := make([]int64, 0, numXc)
	sort.Strings(tokens)
	for i, token := range tokens {
		xcState := currentState[token]
		depth := xcState.Depth
		if depth.Time < oldestUpdate {
			oldestUpdate = depth.Time
		}
		if depth.Time > newestTime {
			newestTime = depth.Time
		}
		updateTimes = append(updateTimes, depth.Time)
		mapifyDepthPoints(depth.Bids, bids, i, numXc)
		mapifyDepthPoints(depth.Asks, asks, i, numXc)
	}
	return &aggregateOrderbook{
		Tokens:      tokens,
		BtcIndex:    bot.BtcIndex,
		Price:       state.GetMutilchainPrice(chainType),
		UpdateTimes: updateTimes,
		Data: aggregateData{
			Time: newestTime,
			Bids: unmapAgOrders(bids, true),
			Asks: unmapAgOrders(asks, false),
		},
		Expiration: oldestUpdate + int64(bot.RequestExpiry.Seconds()),
	}
}

func (bot *ExchangeBot) aggOrderbook() *aggregateOrderbook {
	return bot.aggOrderbookHandler(true)
}

// Make an aggregate orderbook from all depth data.
func (bot *ExchangeBot) aggOrderbookHandler(isDcrUsdtPair bool) *aggregateOrderbook {
	state := bot.State()
	if state == nil {
		return nil
	}
	bids := make(map[int64]agBookPt)
	asks := make(map[int64]agBookPt)

	oldestUpdate := time.Now().Unix()
	var newestTime int64
	// First, grab the tokens for exchanges with depth data so that they can be
	// counted and sorted alphabetically.
	tokens := []string{}
	for token, xcState := range state.DcrBtc {
		if !xcState.HasDepth() || (isDcrUsdtPair && IsDCRBTCExchange(token)) || (!isDcrUsdtPair && !IsDCRBTCExchange(token)) {
			continue
		}
		tokens = append(tokens, token)
	}
	numXc := len(tokens)
	updateTimes := make([]int64, 0, numXc)
	sort.Strings(tokens)
	for i, token := range tokens {
		xcState := state.DcrBtc[token]
		depth := xcState.Depth
		if depth.Time < oldestUpdate {
			oldestUpdate = depth.Time
		}
		if depth.Time > newestTime {
			newestTime = depth.Time
		}
		updateTimes = append(updateTimes, depth.Time)
		mapifyDepthPoints(depth.Bids, bids, i, numXc)
		mapifyDepthPoints(depth.Asks, asks, i, numXc)
	}
	return &aggregateOrderbook{
		Tokens:      tokens,
		BtcIndex:    bot.BtcIndex,
		Price:       state.Price,
		UpdateTimes: updateTimes,
		Data: aggregateData{
			Time: newestTime,
			Bids: unmapAgOrders(bids, true),
			Asks: unmapAgOrders(asks, false),
		},
		Expiration: oldestUpdate + int64(bot.RequestExpiry.Seconds()),
	}
}

func (bot *ExchangeBot) aggOrderbookSubMarketHandler(tokenStr string) *aggregateOrderbook {
	state := bot.State()
	if state == nil {
		return nil
	}
	bids := make(map[int64]agBookPt)
	asks := make(map[int64]agBookPt)

	oldestUpdate := time.Now().Unix()
	var newestTime int64
	tokenInputs := strings.Split(tokenStr, ",")
	// First, grab the tokens for exchanges with depth data so that they can be
	// counted and sorted alphabetically.
	tokens := []string{}
	for token, xcState := range state.DcrBtc {
		if !xcState.HasDepth() || !slices.Contains(tokenInputs, token) {
			continue
		}
		tokens = append(tokens, token)
	}
	numXc := len(tokens)
	updateTimes := make([]int64, 0, numXc)
	sort.Strings(tokens)
	for i, token := range tokens {
		xcState := state.DcrBtc[token]
		depth := xcState.Depth
		if depth.Time < oldestUpdate {
			oldestUpdate = depth.Time
		}
		if depth.Time > newestTime {
			newestTime = depth.Time
		}
		updateTimes = append(updateTimes, depth.Time)
		mapifyDepthPoints(depth.Bids, bids, i, numXc)
		mapifyDepthPoints(depth.Asks, asks, i, numXc)
	}
	return &aggregateOrderbook{
		Tokens:      tokens,
		BtcIndex:    bot.BtcIndex,
		Price:       state.Price,
		UpdateTimes: updateTimes,
		Data: aggregateData{
			Time: newestTime,
			Bids: unmapAgOrders(bids, true),
			Asks: unmapAgOrders(asks, false),
		},
		Expiration: oldestUpdate + int64(bot.RequestExpiry.Seconds()),
	}
}

func (bot *ExchangeBot) QuickSubMarketDepth(tokens string) (chart []byte, err error) {
	chartID := genCacheID(tokens, orderbookKey)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}

	if tokens == aggregatedOrderbookKey {
		agDepth := bot.aggOrderbookHandler(true)
		if agDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else if tokens == aggregatedBTCOrderbookKey {
		agDepth := bot.aggOrderbookHandler(false)
		if agDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else {
		listDepth := bot.aggOrderbookSubMarketHandler(tokens)
		if listDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(listDepth)
	}
	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s depth chart", tokens)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}

// QuickDepth returns the up-to-date depth chart data for the specified
// exchange, pulling from the cache if appropriate.
func (bot *ExchangeBot) QuickDepth(token string) (chart []byte, err error) {
	chartID := genCacheID(token, orderbookKey)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}

	if token == aggregatedOrderbookKey {
		agDepth := bot.aggOrderbookHandler(true)
		if agDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", token)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else if token == aggregatedBTCOrderbookKey {
		agDepth := bot.aggOrderbookHandler(false)
		if agDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", token)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else {
		bot.mtx.Lock()
		defer bot.mtx.Unlock()
		xcState, found := bot.currentState.DcrBtc[token]
		if !found {
			return nil, fmt.Errorf("Failed to find DCR exchange state for %s", token)
		}
		if xcState.Depth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", token)
		}
		chart, err = bot.encodeJSON(&depthResponse{
			BtcIndex:   bot.BtcIndex,
			Price:      bot.currentState.Price,
			Data:       xcState.Depth,
			Expiration: xcState.Depth.Time + int64(bot.RequestExpiry.Seconds()),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s depth chart", token)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}

func (bot *ExchangeBot) MutilchainQuickDepth(token string, chainType string) (chart []byte, err error) {
	chartID := genMutilchainCacheID(chainType, token, orderbookKey)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}

	if token == aggregatedOrderbookKey {
		agDepth := bot.aggMutilchainOrderbook(chainType)
		if agDepth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", token)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else {
		bot.mtx.Lock()
		defer bot.mtx.Unlock()
		currentChainState := bot.currentState.GetMutilchainExchangeState(chainType)
		xcState, found := currentChainState[token]
		if !found {
			return nil, fmt.Errorf("Failed to find DCR exchange state for %s", token)
		}
		if xcState.Depth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", token)
		}
		chart, err = bot.encodeJSON(&depthResponse{
			BtcIndex:   bot.BtcIndex,
			Price:      bot.currentState.GetMutilchainPrice(chainType),
			Data:       xcState.Depth,
			Expiration: xcState.Depth.Time + int64(bot.RequestExpiry.Seconds()),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s depth chart", token)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}

func (bot *ExchangeBot) MutilchainQuickSubMarketDepth(tokens string, chainType string) (chart []byte, err error) {
	chartID := genMutilchainCacheID(chainType, tokens, orderbookKey)
	data, bestVersion, isGood := bot.fetchFromCache(chartID)
	if isGood {
		return data, nil
	}

	if tokens == aggregatedOrderbookKey {
		agDepth := bot.aggMutilchainOrderbook(chainType)
		if agDepth == nil {
			return nil, fmt.Errorf("failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else if strings.Contains(tokens, ",") {
		agDepth := bot.aggMutilchainOrderbookSubMarketHandler(chainType, tokens)
		if agDepth == nil {
			return nil, fmt.Errorf("failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(agDepth)
	} else {
		bot.mtx.Lock()
		defer bot.mtx.Unlock()
		currentChainState := bot.currentState.GetMutilchainExchangeState(chainType)
		xcState, found := currentChainState[tokens]
		if !found {
			return nil, fmt.Errorf("Failed to find DCR exchange state for %s", tokens)
		}
		if xcState.Depth == nil {
			return nil, fmt.Errorf("Failed to find depth for %s", tokens)
		}
		chart, err = bot.encodeJSON(&depthResponse{
			BtcIndex:   bot.BtcIndex,
			Price:      bot.currentState.GetMutilchainPrice(chainType),
			Data:       xcState.Depth,
			Expiration: xcState.Depth.Time + int64(bot.RequestExpiry.Seconds()),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("JSON encode error for %s depth chart", tokens)
	}

	vChart := &versionedChart{
		chartID: chartID,
		dataID:  bestVersion,
		chart:   chart,
	}

	bot.versionedCharts[chartID] = vChart
	return vChart.chart, nil
}
