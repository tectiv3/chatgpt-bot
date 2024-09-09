package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GetCryptoRate is a tool that can do get crypto rate.
type GetCryptoRate struct {
	sessionString string
}

type CoinCap struct {
	Data struct {
		Symbol   string `json:"symbol"`
		PriceUsd string `json:"priceUsd"`
	} `json:"data"`
	Timestamp int64 `json:"timestamp"`
}

func (t GetCryptoRate) Description() string {
	return `Usefull for getting the current rate of various crypto currencies.`
}

func (t GetCryptoRate) Name() string {
	return "DownloadWebsite"
}

func (t GetCryptoRate) Call(ctx context.Context, input string) (string, error) {
	result, err := getCryptoRate(input)
	if err != nil {
		return fmt.Sprintf("error from tool: %s", err.Error()), nil //nolint:nilerr
	}

	return result, nil
}

func getCryptoRate(asset string) (string, error) {
	asset = strings.ToLower(asset)
	format := "$%0.0f"
	switch asset {
	case "btc":
		asset = "bitcoin"
	case "eth":
		asset = "ethereum"
	case "ltc":
		asset = "litecoin"
	case "xrp":
		asset = "ripple"
		format = "$%0.3f"
	case "xlm":
		asset = "stellar"
		format = "$%0.3f"
	case "ada":
		asset = "cardano"
		format = "$%0.3f"
	}
	client := &http.Client{}
	client.Timeout = 10 * time.Second
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.coincap.io/v2/assets/%s", asset), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var symbol CoinCap
	err = json.NewDecoder(resp.Body).Decode(&symbol)
	if err != nil {
		return "", err
	}
	price, _ := strconv.ParseFloat(symbol.Data.PriceUsd, 64)

	return fmt.Sprintf(format, price), nil
}
