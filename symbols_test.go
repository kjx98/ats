package ats

import (
	"testing"
)

func TestInitSymbols(t *testing.T) {
	initSymbols()
	t.Log("initTemp:", initTemp)
}

func TestSymbolsFunc(t *testing.T) {
	initSymbols()
	newSymbolInfo("sh600600")
	newSymbolInfo("cu1903")
	newSymbolInfo("ESZ8")
	newSymbolInfo("ESY8")
	if si, err := GetSymbolInfo("ESY8"); err == nil {
		t.Errorf("ESY8 shouldn't exist, %v", si)
	}
	if si, err := GetSymbolInfo("ESZ8"); err != nil {
		t.Error("not found ESZ8", err)
	} else if np := si.PriceNormal(2810.2534); np != 2810.25 {
		t.Errorf("%s NormalPrice 2810.2534 to %f", si.Ticker, np)
	} else if vv := si.CalcVolume(422000, 2810.25); vv != 60.0 {
		t.Errorf("%s CalcVolume: %f", si.Ticker, vv)
	} else {
		t.Logf("%s digits/volDigits: %d/%d", si.Ticker, si.Digits(), si.VolumeDigits())
	}
	if si, err := GetSymbolInfo("cu1903"); err != nil {
		t.Error("not found cu1903", err)
	} else if np := si.PriceNormal(45320); np != 45300.0 {
		t.Errorf("%s NormalPrice 45320 to %f", si.Ticker, np)
	} else if vv := si.CalcVolume(453210, 45320); vv != 20 {
		t.Errorf("%s CalcVolume: %d", si.Ticker, int(vv))
	} else {
		t.Logf("%s digits/volDigits: %d/%d", si.Ticker, si.Digits(), si.VolumeDigits())
	}
}
