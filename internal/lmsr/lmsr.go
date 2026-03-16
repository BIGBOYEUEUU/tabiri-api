// Package lmsr implements the Logarithmic Market Scoring Rule (LMSR)
// automated market maker for prediction markets.
//
// Key formulas:
//   Cost(qYes, qNo) = b * ln(exp(qYes/b) + exp(qNo/b))
//   P(yes)          = exp(qYes/b) / (exp(qYes/b) + exp(qNo/b))
//   Cost to buy Δq YES shares = Cost(qYes+Δq, qNo) - Cost(qYes, qNo)
//
// The platform's maximum loss per market is bounded at: b * ln(2)
package lmsr

import (
	"math"
)

// Cost returns the LMSR cost function value.
func Cost(qYes, qNo, b float64) float64 {
	return b * math.Log(math.Exp(qYes/b)+math.Exp(qNo/b))
}

// Price returns the current YES probability (0–100).
func Price(qYes, qNo, b float64) float64 {
	eY := math.Exp(qYes / b)
	eN := math.Exp(qNo / b)
	return (eY / (eY + eN)) * 100
}

// CostToBuy returns the KES cost (in kobo units, scaled) to buy `shares`
// of the given side at the current market state.
// Result is in the same unit as the market's internal representation.
// Call with amounts in absolute share units (not KES).
func CostToBuy(qYes, qNo, shares float64, side string, b float64) float64 {
	before := Cost(qYes, qNo, b)
	var after float64
	if side == "yes" {
		after = Cost(qYes+shares, qNo, b)
	} else {
		after = Cost(qYes, qNo+shares, b)
	}
	return after - before
}

// SharesForAmount returns how many shares can be bought for a given KES amount.
// Uses binary search since there is no closed-form inverse.
func SharesForAmount(qYes, qNo, amountKES float64, side string, b float64) float64 {
	// Binary search between 0 and a reasonable upper bound
	lo, hi := 0.0, amountKES*2 // upper bound: can't buy more shares than KES amount

	for i := 0; i < 64; i++ { // 64 iterations gives ~15 decimal places of precision
		mid := (lo + hi) / 2
		cost := CostToBuy(qYes, qNo, mid, side, b)
		if cost < amountKES {
			lo = mid
		} else {
			hi = mid
		}
	}
	return math.Floor(lo*1000) / 1000 // round down to 3 decimal places
}

// PriceImpact returns the % change in YES probability after a trade.
func PriceImpact(qYes, qNo, shares float64, side string, b float64) float64 {
	before := Price(qYes, qNo, b)
	var after float64
	if side == "yes" {
		after = Price(qYes+shares, qNo, b)
	} else {
		after = Price(qYes, qNo+shares, b)
	}
	return math.Abs(after - before)
}

// MaxLoss returns the platform's maximum possible loss for a market.
// This is the amount the platform needs to seed per market.
func MaxLoss(b float64) float64 {
	return b * math.Log(2)
}

// TradeResult holds the computed results of a potential trade.
type TradeResult struct {
	Shares          float64
	CostKES         float64 // raw cost before fees
	FeeKES          float64 // platform fee
	ExciseKES       float64 // government excise
	TotalKES        float64 // what user pays
	PayoutKES       float64 // if they win (shares × 100)
	ProfitKES       float64 // payout - total cost
	ROIPCT          float64 // profit / total × 100
	PriceImpactPct  float64
	NewYesPricePct  float64
}

// ComputeTrade calculates all trade metrics for a given KES amount.
// amountKES is the raw amount the user wants to spend (before fees).
// feeRate is e.g. 0.035, exciseRate is e.g. 0.05.
func ComputeTrade(
	qYes, qNo, b float64,
	side string,
	amountKES, feeRate, exciseRate float64,
) TradeResult {
	// Shares received for the raw amount
	shares := SharesForAmount(qYes, qNo, amountKES, side, b)

	// Fees
	fee    := math.Max(5, amountKES*feeRate) // minimum fee = KES 5
	excise := amountKES * exciseRate
	total  := amountKES + fee + excise

	// Payout if win (KES 100 per share)
	payout := shares * 100
	profit := payout - total
	roi    := 0.0
	if total > 0 {
		roi = (profit / total) * 100
	}

	// Price impact
	impact   := PriceImpact(qYes, qNo, shares, side, b)
	newPrice := Price(qYes, qNo, b) // current — actual new price is post-trade
	if side == "yes" {
		newPrice = Price(qYes+shares, qNo, b)
	} else {
		newPrice = Price(qYes, qNo+shares, b)
	}

	return TradeResult{
		Shares:         shares,
		CostKES:        amountKES,
		FeeKES:         fee,
		ExciseKES:      excise,
		TotalKES:       total,
		PayoutKES:      payout,
		ProfitKES:      profit,
		ROIPCT:         roi,
		PriceImpactPct: impact,
		NewYesPricePct: newPrice,
	}
}
