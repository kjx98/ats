package ats

// No Level2 quotes yet
type Quotes struct {
	TodayOpen float64
	TodayHigh float64
	TodayLow  float64
	Pclose    float64
	Last      float64
	Volume    int64
	Turnover  float64
	Bid       float64
	Ask       float64
	BidVol    int64
	AskVol    int64
}

func (s *SymbolInfo) GetQuotes() Quotes {
	return s.Quotes
}

func UpdateQuotes(sym string, qq *Quotes) {
	if si, err := GetSymbolInfo(sym); err != nil {
		return
	} else {
		si.Quotes = *qq
	}
}

func UpdateLastSales(sym string, last float64, vol float64) {
	if si, err := GetSymbolInfo(sym); err != nil {
		return
	} else {
		if si.VolDigits > 0 {
			vol /= dMulti[si.VolDigits]
		}
		nVol := int64(vol)
		if nVol < si.Volume {
			// volume must same or increased
			return
		}
		si.Last = last
		si.Volume = nVol
		if si.TodayLow == 0 || last < si.TodayLow {
			si.TodayLow = last
		}
		if si.TodayHigh < last {
			si.TodayHigh = last
		}
	}
}

func UpdateBidAsk(sym string, bid, ask float64) {
	if si, err := GetSymbolInfo(sym); err != nil {
		return
	} else {
		si.Bid = bid
		si.Ask = ask
	}
}
