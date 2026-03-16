package lmsr

import (
	"math"
	"testing"
)

func TestPrice(t *testing.T) {
	// At q_yes = q_no = 0 and any b, price should be 50%
	p := Price(0, 0, 100)
	if math.Abs(p-50) > 0.001 {
		t.Errorf("expected 50%% at equal shares, got %.4f", p)
	}
}

func TestPriceMovesWithTrades(t *testing.T) {
	b := 100.0
	p0 := Price(0, 0, b)    // 50%
	p1 := Price(50, 0, b)   // should be > 50%

	if p1 <= p0 {
		t.Errorf("price should increase when YES shares increase: p0=%.2f p1=%.2f", p0, p1)
	}
}

func TestCostToBuyPositive(t *testing.T) {
	cost := CostToBuy(0, 0, 10, "yes", 100)
	if cost <= 0 {
		t.Errorf("cost to buy should be positive, got %.4f", cost)
	}
}

func TestSharesForAmount(t *testing.T) {
	b := 100.0
	amountKES := 1000.0
	shares := SharesForAmount(0, 0, amountKES, "yes", b)

	if shares <= 0 {
		t.Errorf("shares should be positive, got %.4f", shares)
	}

	// Verify cost of those shares ≈ amountKES (within rounding)
	cost := CostToBuy(0, 0, shares, "yes", b)
	if math.Abs(cost-amountKES) > 1 {
		t.Errorf("share cost %.4f should be close to %.4f", cost, amountKES)
	}
}

func TestMaxLoss(t *testing.T) {
	// For b=100, max loss = 100 * ln(2) ≈ 69.3
	ml := MaxLoss(100)
	expected := 100 * math.Log(2)
	if math.Abs(ml-expected) > 0.001 {
		t.Errorf("expected %.4f, got %.4f", expected, ml)
	}
}

func TestComputeTrade(t *testing.T) {
	result := ComputeTrade(0, 0, 100, "yes", 1000, 0.035, 0.05)

	if result.Shares <= 0 {
		t.Error("shares should be positive")
	}
	if result.TotalKES <= result.CostKES {
		t.Error("total (with fees) should be > raw cost")
	}
	if result.PayoutKES != result.Shares*100 {
		t.Errorf("payout should be shares × 100")
	}
}

// Regression: buying YES at 50% for KES 1000 with b=100
// should give roughly 13-14 shares (at ~KES 72/share after LMSR pricing)
func TestReasonableShareCount(t *testing.T) {
	shares := SharesForAmount(0, 0, 1000, "yes", 100)
	if shares < 10 || shares > 30 {
		t.Errorf("expected ~13-14 shares for KES 1000 at b=100, got %.3f", shares)
	}
}
