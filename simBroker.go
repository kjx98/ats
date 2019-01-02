package ats

import (
	"encoding/csv"
	"errors"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kjx98/avl"
	"github.com/kjx98/golib/julian"
	"github.com/kjx98/golib/to"
)

type account struct {
	fundStart  float64
	equity     float64
	balance    float64
	fund       float64
	margin     float64 // freeze fund for margin
	trades     int
	winTrades  int
	lossTrades int
	profit     float64
	loss       float64

	evChan chan<- QuoteEvent
	orders []int
	pos    []PositionType
}

type simOrderType struct {
	simBroker
	oid   int
	price int32
	OrderType
}

type orderBook struct {
	bids, asks *avl.Tree
}

type simTick struct {
	curP  int
	ticks []Tick
}

type simTickFX struct {
	curP  int
	ticks []TickFX
}

func (sti *simTick) Len() int {
	return len(sti.ticks)
}

func (sti *simTick) Time() DateTimeMs {
	curP := sti.curP
	if curP > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return sti.ticks[curP].Time.DateTimeMs()
}

func (sti *simTick) TimeAt(i int) DateTimeMs {
	if i > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return sti.ticks[i].Time.DateTimeMs()
}

func (sti *simTick) TickValue() (bid, ask, last int32, vol uint32) {
	curP := sti.curP
	if curP > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return 0, 0, sti.ticks[curP].Last, sti.ticks[curP].Volume
}

func (sti *simTick) Next() error {
	sti.curP++
	if sti.curP >= len(sti.ticks) {
		return io.EOF
	}
	return nil
}

func (sti *simTickFX) Len() int {
	return len(sti.ticks)
}

func (sti *simTickFX) Time() DateTimeMs {
	curP := sti.curP
	if curP > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return sti.ticks[curP].Time
}

func (sti *simTickFX) TimeAt(i int) DateTimeMs {
	if i > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return sti.ticks[i].Time
}

func (sti *simTickFX) TickValue() (bid, ask, last int32, vol uint32) {
	curP := sti.curP
	if curP > len(sti.ticks) {
		panic("Out of simTick bound")
	}
	return sti.ticks[curP].Bid, sti.ticks[curP].Ask, 0, 0
}

func (sti *simTickFX) Next() error {
	sti.curP++
	if sti.curP >= len(sti.ticks) {
		return io.EOF
	}
	return nil
}

type simTicker interface {
	Len() int
	Time() DateTimeMs
	TimeAt(i int) DateTimeMs
	Next() error
	TickValue() (bid, ask, last int32, vol uint32)
}

func bidCompare(a, b interface{}) int {
	ora, ok := a.(*simOrderType)
	// maybe panic, if not simOrderType
	if !ok {
		return 0
	}
	orb, ok := b.(*simOrderType)
	if !ok {
		return 0
	}
	if ora.price == orb.price {
		return ora.oid - orb.oid
	}
	// low price, low priority
	return int(orb.price) - int(ora.price)
}

func askCompare(a, b interface{}) int {
	ora, ok := a.(*simOrderType)
	// maybe panic, if not simOrderType
	if !ok {
		return 0
	}
	orb, ok := b.(*simOrderType)
	if !ok {
		return 0
	}
	if ora.price == orb.price {
		return ora.oid - orb.oid
	}
	// low price, low priority
	return int(ora.price) - int(orb.price)
}

var acctLock sync.RWMutex
var nAccounts int
var simAccounts = map[simBroker]*account{}
var orderLock sync.RWMutex
var nOrders int
var simOrders = []simOrderType{}
var startTime, endTime timeT64
var simCurrent DateTimeMs
var simVmLock sync.RWMutex
var simTickMap = map[SymbolKey]simTicker{}
var simTickRun map[SymbolKey]simTicker

// simStatus should be atomic
var simStatus int32
var maxAllocHeap uint64
var maxSysHeap uint64
var timeAtMaxAlloc DateTimeMs
var onceLoad sync.Once

// simSymbolQ symbol fKey map
var simSymbolsQ = map[int]*Quotes{}

// orderBook map with symbol key
var simOrderBook map[string]orderBook

const (
	VmIdle int32 = iota
	VmStart
	VmRunning
	VmStoping
)

var (
	vmStatusErr     = errors.New("simBroker VM status error")
	errTickNonExist = errors.New("Tick Data not exist")
	errTickOrder    = errors.New("Tick Data order error")
)

type simBroker int

func (b simBroker) Open(ch chan<- QuoteEvent) (Broker, error) {
	acctLock.Lock()
	defer acctLock.Unlock()
	var acct = account{evChan: ch}
	bb := simBroker(nAccounts)
	nAccounts++
	simAccounts[bb] = &acct
	return bb, nil
}

func simLoadSymbols() {
	onceLoad.Do(func() {
		if fd, err := os.Open("universe.csv"); err != nil {
			panic("open universe.csv error")
		} else {
			defer fd.Close()
			csvR := csv.NewReader(fd)
			line, err := csvR.Read()
			for err == nil {
				// process a line
				if line[0] != "" && len(line) > 1 {
					newSymbolInfo(line[0])
					if si, err := GetSymbolInfo(line[0]); err == nil {
						// try load tick, min, day data
						var st, dt julian.JulianDay
						var bNeedForge = true
						if len(line) > 2 {
							st = julian.FromUint32(uint32(to.Int(line[2])))
							if len(line) > 3 {
								dt = julian.FromUint32(uint32(to.Int(line[3])))
							}
						}
						if si.IsForex {
							if strings.Contains(line[1], "t") {
								if res, err := LoadTickFX(line[0], st, dt, 0); err == nil {
									// load to sim
									var tickD = simTickFX{}
									tickD.ticks = res
									simTickMap[si.FastKey()] = &tickD
									bNeedForge = false
								}
							}
						} else {
							if strings.Contains(line[1], "t") {
								// try load ticks for Non FX
							}
						}
						if strings.Contains(line[1], "m") {
							// loadMindata
							if si.IsForex {
								LoadBarFX(line[0], Min1, st, dt)
							} else {
								// try load Min5
							}
						}
						if strings.Contains(line[1], "d") {
							// load daily Bar
							if si.IsForex {
								// load FX daily
							} else {
								LoadDayBar(line[0], Daily, st, dt)
							}
						}
						if bNeedForge {
							// no tick, forge tick from Min1/Min5 or Daily
							forgeTicks(&si)
						}
					}
				}
				line, err = csvR.Read()
			}
		}
	})
	simTickRun = map[SymbolKey]simTicker{}
	for k, v := range simTickMap {
		simTickRun[k] = v
	}
}

func forgeTicks(si *SymbolInfo) {
	forgeTicksFromBar := func(cc cacheTAer, period Period) {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		if si.IsForex {
			var tickD = simTickFX{}
			for i := 0; i < cc.Len(); i++ {
				var atick TickFX
				ti, o, h, l, c, _ := cc.BarValue(i)
				atick.Time = ti.DateTimeMs()
				atick.Bid = o
				atick.Ask = o + si.DefSpread
				tickD.ticks = append(tickD.ticks, atick)
				hto := r.Int63() % int64(period)
				lto := r.Int63() % int64(period)
				if hto > lto {
					atick.Time = (ti + timeT64(lto)).DateTimeMs()
					atick.Bid = l
					atick.Ask = l + si.DefSpread
					tickD.ticks = append(tickD.ticks, atick)
					atick.Time = (ti + timeT64(hto)).DateTimeMs()
					atick.Bid = h
					atick.Ask = h + si.DefSpread
					tickD.ticks = append(tickD.ticks, atick)
				} else {
					atick.Time = (ti + timeT64(hto)).DateTimeMs()
					atick.Bid = h
					atick.Ask = h + si.DefSpread
					tickD.ticks = append(tickD.ticks, atick)
					atick.Time = (ti + timeT64(lto)).DateTimeMs()
					atick.Bid = l
					atick.Ask = l + si.DefSpread
					tickD.ticks = append(tickD.ticks, atick)
				}
				// last for close
				atick.Time = (ti + timeT64(period)).DateTimeMs() - 1
				atick.Bid = c
				atick.Ask = c + si.DefSpread
				tickD.ticks = append(tickD.ticks, atick)
			}
			simTickMap[si.FastKey()] = &tickD
		} else {
			var tickD = simTick{}
			for i := 0; i < cc.Len(); i++ {
				var atick Tick
				ti, o, h, l, c, vol := cc.BarValue(i)
				atick.Time = timeT32(ti)
				atick.Last = o
				atick.Volume = uint32(vol * 3 / 8)
				tickD.ticks = append(tickD.ticks, atick)
				hto := r.Int63() % int64(period)
				lto := r.Int63() % int64(period)
				if hto > lto {
					atick.Time = timeT32(ti + timeT64(lto))
					atick.Last = l
					atick.Volume = uint32(vol / 8)
					tickD.ticks = append(tickD.ticks, atick)
					atick.Time = timeT32(ti + timeT64(hto))
					atick.Last = h
					atick.Volume = uint32(vol / 8)
					tickD.ticks = append(tickD.ticks, atick)
				} else {
					atick.Time = timeT32(ti + timeT64(hto))
					atick.Last = h
					atick.Volume = uint32(vol / 8)
					tickD.ticks = append(tickD.ticks, atick)
					atick.Time = timeT32(ti + timeT64(lto))
					atick.Last = l
					atick.Volume = uint32(vol / 8)
					tickD.ticks = append(tickD.ticks, atick)
				}
				// last for close
				atick.Time = timeT32(ti+timeT64(period)) - 1
				atick.Last = c
				atick.Volume = uint32(vol * 3 / 8)
				tickD.ticks = append(tickD.ticks, atick)
			}
			simTickMap[si.FastKey()] = &tickD
		}
	}
	if si.IsForex {
		if cc, ok := cacheMinFX[si.Ticker]; ok {
			// forge via FX Min1
			forgeTicksFromBar(&cc, Min1)
			return
		}
	} else {
		if cc, ok := cacheMinTA[si.Ticker]; ok {
			// forge via Min5
			forgeTicksFromBar(&cc, Min5)
			return
		}
	}
	if cc, ok := cacheDayTA[si.Ticker]; ok {
		// forge via Daily
		forgeTicksFromBar(&cc, Daily)
		return
	}
}

func LoadRunTick(sym string) (simTicker, error) {
	if si, err := GetSymbolInfo(sym); err != nil {
		return nil, err
	} else if v, ok := simTickRun[si.FastKey()]; ok {
		return v, nil
	}
	return nil, errTickNonExist
}

func ValidateTick(sym string) error {
	if si, err := GetSymbolInfo(sym); err != nil {
		return err
	} else if v, ok := simTickMap[si.FastKey()]; ok {
		var oldTi DateTimeMs
		for i := 0; i < v.Len(); i++ {
			if ti := v.TimeAt(i); ti < oldTi {
				return errTickOrder
			} else {
				oldTi = ti
			}
		}
	} else {
		return errTickNonExist
	}
	return nil
}

// every instance of VM should be with same configure
func (b simBroker) Start(c Config) error {
	// read Config, ...
	// start goroutine for simulate/backtesting
	switch atomic.LoadInt32(&simStatus) {
	case VmIdle:
	case VmStart, VmRunning:
		return nil
	default:
		return vmStatusErr
	}
	simVmLock.Lock()
	defer simVmLock.Unlock()
	maxAllocHeap = 0
	maxSysHeap = 0
	timeAtMaxAlloc = 0
	atomic.StoreInt32(&simStatus, VmStart)
	// load Bars
	// build ticks
	simLoadSymbols()
	// start Tick feed goroutine
	// always run tick
	/*
		if c.GetInt("RunTick", 0) != 0 {
			// go routint tick matcher
		} else {
			// go bar matcher
		}
	*/
	atomic.StoreInt32(&simStatus, VmRunning)
	return nil
}

func (b simBroker) Stop() error {
	switch atomic.LoadInt32(&simStatus) {
	case VmIdle, VmStoping:
		return nil
	case VmRunning:
	default:
		return vmStatusErr
	}
	simVmLock.Lock()
	defer simVmLock.Unlock()
	atomic.StoreInt32(&simStatus, VmStoping)
	// stop Bar feed
	atomic.StoreInt32(&simStatus, VmIdle)
	return nil
}

func (b simBroker) SubscribeQuotes(qq []QuoteSubT) error {
	if atomic.LoadInt32(&simStatus) != VmIdle {
		return vmStatusErr
	}
	// prepare Bars
	// maybe Once load?
	simVmLock.Lock()
	defer simVmLock.Unlock()
	// update QuotesPtr only not subscribed
	for _, qs := range qq {
		if si, err := GetSymbolInfo(qs.Symbol); err != nil {
			continue
		} else {
			if _, ok := simSymbolsQ[si.fKey]; !ok {
				simSymbolsQ[si.fKey] = qs.QuotesPtr
			}
		}
	}
	return nil
}

func (b simBroker) Equity() float64 {
	acct := simAccounts[b]
	return acct.equity
}

func (b simBroker) Balance() float64 {
	acct := simAccounts[b]
	return acct.balance
}

func (b simBroker) Cash() float64 {
	acct := simAccounts[b]
	return acct.fund
}

func (b simBroker) FreeMargin() float64 {
	acct := simAccounts[b]
	return acct.equity - acct.margin
}

func (b simBroker) SendOrder(sym string, dir OrderDirT, qty int, prc float64, stopL float64) int {
	simVmLock.Lock()
	defer simVmLock.Unlock()
	// tobe fix
	// verify, put to orderbook
	return 0
}

func (b simBroker) CancelOrder(oid int) {
	//acct := simAccounts[b]
	if oid >= nOrders {
		return
	}
	simVmLock.Lock()
	defer simVmLock.Unlock()
	// remove order from orderbook
}

func (b simBroker) CloseOrder(oId int) {
	//acct := simAccounts[b]
	// if open, close with market
	// if stoploss, remove stoploss, change to market
	if oId >= nOrders {
		return
	}
	simVmLock.Lock()
	defer simVmLock.Unlock()
	// if order open or partfill, changed to market order
}

func (b simBroker) GetOrder(oId int) *OrderType {
	if oId >= nOrders {
		return nil
	}
	orderLock.RLock()
	defer orderLock.RUnlock()
	return &simOrders[oId].OrderType
}

func (b simBroker) GetOrders() []int {
	acct := simAccounts[b]
	return acct.orders
}

func (b simBroker) GetPosition(sym string) (vPos PositionType) {
	acct := simAccounts[b]
	for _, v := range acct.pos {
		if v.Symbol == sym {
			vPos = v
			return
		}
	}
	return
}

func (b simBroker) GetPositions() []PositionType {
	acct := simAccounts[b]
	return acct.pos
}

//go:noinline
func (b simBroker) TimeCurrent() DateTimeMs {
	return simCurrent
}

var simTrader simBroker

func init() {
	RegisterBroker("simBroker", simTrader)
}
