package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

// ChartResponse represents the JSON response from the Yahoo Finance chart API.
type ChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open  []float64 `json:"open"`
					Close []float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error interface{} `json:"error"`
	} `json:"chart"`
}

// analyzeTimeframe performs the calculations for a given ticker and range (e.g., "ytd", "1y", "3y", "5y", "10y").
func analyzeTimeframe(ticker, rangeStr string) (avgAbsPct float64, avgMonthlyRedDays float64, err error) {
	// Build the Yahoo Finance chart API URL.
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?range=%s&interval=1d", ticker, rangeStr)

	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("Error creating HTTP request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("Error making HTTP request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("Received status code %d", resp.StatusCode)
	}

	var chartResp ChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&chartResp); err != nil {
		return 0, 0, fmt.Errorf("Error decoding JSON: %v", err)
	}
	if len(chartResp.Chart.Result) == 0 {
		return 0, 0, fmt.Errorf("No result in chart response")
	}

	result := chartResp.Chart.Result[0]
	timestamps := result.Timestamp
	opens := result.Indicators.Quote[0].Open
	closes := result.Indicators.Quote[0].Close

	if len(timestamps) != len(opens) || len(timestamps) != len(closes) {
		return 0, 0, fmt.Errorf("Mismatch in data lengths")
	}

	// First pass: Compute the average absolute daily move in percentage.
	var totalAbsPct float64
	validCount := 0.0
	dailyPct := make([]float64, len(timestamps))
	monthDays := make(map[string]int)

	for i, ts := range timestamps {
		if opens[i] == 0 {
			continue
		}
		movePct := ((closes[i] - opens[i]) / opens[i]) * 100.0
		dailyPct[i] = movePct
		totalAbsPct += math.Abs(movePct)
		validCount++

		t := time.Unix(ts, 0)
		monthKey := t.Format("2006-01")
		monthDays[monthKey]++
	}
	if validCount == 0 {
		return 0, 0, fmt.Errorf("No valid trading days found")
	}
	avgAbsPct = totalAbsPct / validCount

	// Red day threshold: a red day is one where the daily move is less than -60% of the average absolute move.
	threshold := -1 * avgAbsPct

	// Second pass: Count red days per month.
	redDaysByMonth := make(map[string]int)
	for i, ts := range timestamps {
		if opens[i] == 0 {
			continue
		}
		if dailyPct[i] < threshold {
			t := time.Unix(ts, 0)
			monthKey := t.Format("2006-01")
			redDaysByMonth[monthKey]++
		}
	}

	// Calculate average monthly red days.
	totalRedDays := 0
	monthsCounted := 0
	for month := range monthDays {
		monthsCounted++
		totalRedDays += redDaysByMonth[month]
	}
	avgMonthlyRedDays = math.Round(float64(totalRedDays) / float64(monthsCounted))

	return avgAbsPct, avgMonthlyRedDays, nil
}

type TimeframeResult struct {
	Ticker            string
	Timeframe         string
	AvgAbsDailyMove   float64
	AvgMonthlyRedDays float64
	Err               error
}

func main() {
	// Accept multiple tickers as a comma-separated list.
	tickersFlag := flag.String("tickers", "", "Comma-separated list of ticker symbols (e.g., NVDA,GOOG,MSFT)")
	flag.Parse()
	if *tickersFlag == "" {
		fmt.Println("Please provide at least one ticker using the -tickers flag.")
		os.Exit(1)
	}
	tickers := strings.Split(*tickersFlag, ",")
	for i := range tickers {
		tickers[i] = strings.TrimSpace(tickers[i])
	}

	// Define the timeframes: ytd, 1y, 3y, 5y, and 10y.
	timeframes := []string{"ytd", "1y", "3y", "5y", "10y"}
	resultsChan := make(chan TimeframeResult, len(tickers)*len(timeframes))
	var wg sync.WaitGroup

	// Launch a goroutine for each ticker and timeframe.
	for _, tkr := range tickers {
		for _, tf := range timeframes {
			wg.Add(1)
			go func(ticker, timeframe string) {
				defer wg.Done()
				avgPct, avgRed, err := analyzeTimeframe(ticker, timeframe)
				resultsChan <- TimeframeResult{
					Ticker:            ticker,
					Timeframe:         timeframe,
					AvgAbsDailyMove:   avgPct,
					AvgMonthlyRedDays: avgRed,
					Err:               err,
				}
			}(tkr, tf)
		}
	}

	wg.Wait()
	close(resultsChan)

	// Arrange results in a matrix: rows = timeframes, columns = tickers.
	matrix := make(map[string]map[string]string)
	for _, tf := range timeframes {
		matrix[tf] = make(map[string]string)
	}
	for res := range resultsChan {
		if res.Err != nil {
			matrix[res.Timeframe][res.Ticker] = "ERR"
		} else {
			// Format cell: "Avg Abs: X.XX% / Red: Y days"
			matrix[res.Timeframe][res.Ticker] = fmt.Sprintf("Avg Abs: %.2f%% / Red: %.0f days", res.AvgAbsDailyMove, res.AvgMonthlyRedDays)
		}
	}

	// Print the table with rows as timeframes and columns as tickers.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// Header row.
	header := "Timeframe\t"
	for _, tkr := range tickers {
		header += fmt.Sprintf("%s\t", tkr)
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, "---------\t"+strings.Repeat("---------\t", len(tickers)))
	// Data rows.
	for _, tf := range timeframes {
		row := fmt.Sprintf("%s\t", tf)
		for _, tkr := range tickers {
			cell, ok := matrix[tf][tkr]
			if !ok {
				cell = "N/A"
			}
			row += fmt.Sprintf("%s\t", cell)
		}
		fmt.Fprintln(w, row)
	}
	w.Flush()
}
