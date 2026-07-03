// Package demo orchestrates the end-to-end story. It brings up each bank's OWN
// signing-router (holding only that bank's key) and deposit-svc as separate
// processes, then drives the scenario purely through the deposit-svc HTTP APIs:
//
//  1. Bank A (issuer) authorizes Bank B's wallet + the merchant (onboarding)
//  2. Bank A mints deposit tokens into Bank B's wallet
//  3. Bank B (holder) transfers tokens to a merchant in real time
//  4. Reconcile issuer backing == circulating supply, and prove key isolation
package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"besu-interbank-demo/internal/config"
)

// ANSI helpers
const (
	amber = "\x1b[38;5;179m"
	teal  = "\x1b[38;5;80m"
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	green = "\x1b[38;5;71m"
	red   = "\x1b[38;5;167m"
	reset = "\x1b[0m"
)

func tint(c, s string) string { return c + s + reset }

type proc struct {
	name string
	cmd  *exec.Cmd
}

// Run executes the full demo, spawning sibling binaries built alongside this one.
func Run(ctx context.Context) error {
	dep, err := config.LoadDeployment()
	if err != nil {
		return err
	}

	fmt.Printf("\n%s══════ Besu Interbank Deposit-Token Demo ══════%s\n", bold, reset)
	fmt.Printf("%s  chainId %d · deposit token @ %s%s\n", dim, dep.ChainID, dep.DepositToken.Hex(), reset)
	fmt.Printf("%s  Each bank runs its own signing-router (its key) + deposit-svc (no key).%s\n\n", dim, reset)

	// Local mode spawns the four services as sibling processes. In Docker/K8s they
	// already run as their own containers/pods, so set DEMO_SPAWN=false and this
	// binary just drives the scenario over HTTP.
	if os.Getenv("DEMO_SPAWN") != "false" {
		binDir, err := binDir()
		if err != nil {
			return err
		}
		procs := []proc{
			spawn(binDir, "signing-router", tint(amber, "router-A"), amber,
				"PORT="+itoa(config.BankA.SigningRouterPort), "LABEL=Bank A router", "ROUTER_KEYS="+config.BankA.KeyID),
			spawn(binDir, "signing-router", tint(teal, "router-B"), teal,
				"PORT="+itoa(config.BankB.SigningRouterPort), "LABEL=Bank B router", "ROUTER_KEYS="+config.BankB.KeyID),
			spawn(binDir, "deposit-svc", tint(amber, "deposit-A"), amber, "BANK_ID="+config.BankA.ID),
			spawn(binDir, "deposit-svc", tint(teal, "deposit-B"), teal, "BANK_ID="+config.BankB.ID),
		}
		defer func() {
			for _, p := range procs {
				if p.cmd.Process != nil {
					_ = p.cmd.Process.Kill()
				}
			}
		}()
	}

	for _, u := range []string{config.BankA.SigningRouterURL, config.BankB.SigningRouterURL, config.BankA.DepositSvcURL, config.BankB.DepositSvcURL} {
		if err := waitHealthy(ctx, u); err != nil {
			return err
		}
	}
	time.Sleep(300 * time.Millisecond)

	// Token header
	var info struct {
		Token struct {
			Name, Symbol string
			Decimals     int
		} `json:"token"`
	}
	if err := getJSON(ctx, config.BankA.DepositSvcURL+"/info", &info); err != nil {
		return err
	}
	fmt.Printf("  Token: %s%s%s (%s), %d decimals\n", bold, info.Token.Name, reset, info.Token.Symbol, info.Token.Decimals)
	showBalances(ctx, "Opening balances")

	// ① Onboarding
	fmt.Printf("\n%s  ① Bank A authorizes Bank B's wallet and the merchant%s\n", amber, reset)
	for _, t := range []struct {
		who  string
		addr string
	}{{"Bank B wallet", config.BankB.Address.Hex()}, {"Merchant", config.Merchant.Hex()}} {
		var res []txResult
		if err := postJSON(ctx, config.BankA.DepositSvcURL+"/authorize", map[string]string{"account": t.addr}, &res); err != nil {
			return err
		}
		fmt.Printf("%s    onboarded %s%s\n", dim, t.who, reset)
		for _, r := range res {
			r.print(amber)
		}
	}

	// ② Mint
	fmt.Printf("\n%s  ② Bank A mints %s1,000,000 WFUSD%s → Bank B's wallet%s\n", amber, bold, reset+amber, reset)
	var mint txResult
	if err := postJSON(ctx, config.BankA.DepositSvcURL+"/mint",
		map[string]string{"to": config.BankB.Address.Hex(), "amount": wfusd("1000000")}, &mint); err != nil {
		return err
	}
	mint.print(amber)
	showBalances(ctx, "After mint")

	// ③ Transfer
	fmt.Printf("\n%s  ③ Bank B transfers %s250,000 WFUSD%s → Merchant (real-time)%s\n", teal, bold, reset+teal, reset)
	var xfer txResult
	if err := postJSON(ctx, config.BankB.DepositSvcURL+"/transfer",
		map[string]string{"to": config.Merchant.Hex(), "amount": wfusd("250000")}, &xfer); err != nil {
		return err
	}
	xfer.print(teal)
	supply, rl := showBalances(ctx, "After transfer")

	// ④ Reconcile + isolation
	fmt.Printf("\n%s  ④ Reconcile & key-isolation checks%s\n", bold, reset)
	if supply == rl {
		fmt.Printf("    %s✓%s ReserveLedger backing %s == deposit-token supply %s\n", green, reset, fmtWFUSD(rl), fmtWFUSD(supply))
	} else {
		fmt.Printf("    %s✗%s backing %s != supply %s\n", red, reset, fmtWFUSD(rl), fmtWFUSD(supply))
	}

	// Bank B (holder) must not be able to mint.
	if err := postJSON(ctx, config.BankB.DepositSvcURL+"/mint", map[string]string{"to": config.BankB.Address.Hex(), "amount": "1"}, &struct{}{}); err != nil {
		fmt.Printf("    %s✓%s Bank B cannot mint %s(%s)%s\n", green, reset, dim, err.Error(), reset)
	} else {
		fmt.Printf("    %s✗%s Bank B was able to mint — SHOULD NOT HAPPEN\n", red, reset)
	}

	// Bank A's signing-router must not hold Bank B's key.
	code := signWithWrongKey(ctx)
	if code == http.StatusForbidden {
		fmt.Printf("    %s✓%s Bank A's router refuses to sign with Bank B's key %s(403 — key not held)%s\n", green, reset, dim, reset)
	} else {
		fmt.Printf("    %s✗%s Bank A's router signed for Bank B — SHOULD NOT HAPPEN (%d)\n", red, reset, code)
	}

	fmt.Printf("\n%s  ✓ Demo complete — two banks, two keys, tokens moved in real time.%s\n\n", green, reset)
	return nil
}

// ─── scenario helpers ─────────────────────────────────────────────────────────

type txResult struct {
	Op          string `json:"op"`
	TxHash      string `json:"txHash"`
	Status      string `json:"status"`
	BlockNumber string `json:"blockNumber"`
	SignedBy    string `json:"signedBy"`
}

func (t txResult) print(c string) {
	mark := tint(green, "✓")
	if t.Status != "success" {
		mark = tint(red, "✗ reverted")
	}
	hash := t.TxHash
	if len(hash) > 18 {
		hash = hash[:18] + "…"
	}
	signer := t.SignedBy
	if len(signer) > 10 {
		signer = signer[:10] + "…"
	}
	fmt.Printf("    %s %-24s %sblk#%s%s  %s%s%s  signed by %s\n",
		mark, t.Op, dim, t.BlockNumber, reset, dim, hash, reset, tint(c, signer))
}

func showBalances(ctx context.Context, title string) (supply, rl string) {
	bB := balanceOf(ctx, config.BankB.Address.Hex())
	mer := balanceOf(ctx, config.Merchant.Hex())
	var info struct {
		Token struct {
			TotalSupply         string `json:"totalSupply"`
			ReserveLedgerSupply string `json:"reserveLedgerSupply"`
		} `json:"token"`
	}
	_ = getJSON(ctx, config.BankA.DepositSvcURL+"/info", &info)
	fmt.Printf("\n%s  %s%s\n", bold, title, reset)
	fmt.Printf("    %s   %16s   %s%s%s\n", tint(teal, "Bank B wallet"), fmtWFUSD(bB), dim, config.BankB.Address.Hex(), reset)
	fmt.Printf("    Merchant          %16s   %s%s%s\n", fmtWFUSD(mer), dim, config.Merchant.Hex(), reset)
	fmt.Printf("%s    ─ deposit-token supply %s  ·  ReserveLedger backing %s%s\n",
		dim, fmtWFUSD(info.Token.TotalSupply), fmtWFUSD(info.Token.ReserveLedgerSupply), reset)
	return info.Token.TotalSupply, info.Token.ReserveLedgerSupply
}

func balanceOf(ctx context.Context, addr string) string {
	var out struct {
		Raw string `json:"raw"`
	}
	_ = getJSON(ctx, config.BankA.DepositSvcURL+"/balance?account="+addr, &out)
	if out.Raw == "" {
		return "0"
	}
	return out.Raw
}

func signWithWrongKey(ctx context.Context) int {
	body, _ := json.Marshal(map[string]string{"keyId": config.BankB.KeyID, "hash": "0x" + strings.Repeat("11", 32)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, config.BankA.SigningRouterURL+"/sign", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// ─── process + http plumbing ───────────────────────────────────────────────────

func binDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func spawn(binDir, bin, label, color string, env ...string) proc {
	cmd := exec.Command(filepath.Join(binDir, bin))
	cmd.Env = append(os.Environ(), env...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	go pipePrefixed(stdout, label, dim)
	go pipePrefixed(stderr, label, red)
	if err := cmd.Start(); err != nil {
		fmt.Printf("%s  ✗ failed to start %s: %v%s\n", red, bin, err, reset)
	}
	return proc{name: bin, cmd: cmd}
}

func pipePrefixed(r io.Reader, label, color string) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n") {
				fmt.Printf("  ┊ %s %s%s%s\n", label, color, line, reset)
			}
		}
		if err != nil {
			return
		}
	}
}

func waitHealthy(ctx context.Context, base string) error {
	for i := 0; i < 80; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("service at %s never became healthy", base)
}

func getJSON(ctx context.Context, url string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func postJSON(ctx context.Context, url string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = string(data)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ─── formatting ────────────────────────────────────────────────────────────────

// wfusd converts a whole-token string ("1000000") to base units (6 decimals).
func wfusd(whole string) string {
	n, _ := new(big.Int).SetString(whole, 10)
	n.Mul(n, big.NewInt(1_000_000))
	return n.String()
}

// fmtWFUSD renders base units as a human "N WFUSD" string.
func fmtWFUSD(base string) string {
	n, ok := new(big.Int).SetString(base, 10)
	if !ok {
		n = big.NewInt(0)
	}
	whole := new(big.Int).Quo(n, big.NewInt(1_000_000))
	// thousands separators
	s := whole.String()
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",") + " WFUSD"
}

func itoa(i int) string { return strconv.Itoa(i) }
