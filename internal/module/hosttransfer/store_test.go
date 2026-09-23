package hosttransfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is the injected now; tests move it by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// seqGen hands out codes in order: C0000000, C0000001, ...
func seqGen() func() (string, error) {
	var mu sync.Mutex
	n := 0
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		code := fmt.Sprintf("C%07d", n)
		n++
		return code, nil
	}
}

func newTestStore() (*Store, *fakeClock) {
	clk := newFakeClock()
	return newStore(clk.now, seqGen()), clk
}

func payloadN(n int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`[{"ip":"10.0.0.%d","token":"tok-%d"}]`, n, n))
}

// wrongCode is a well-formed code no test ever creates.
const wrongCode = "ZZZZZZZZ"

// Test 1.
func TestCreateThenRedeemReturnsThePayloadOnce(t *testing.T) {
	s, clk := newTestStore()
	code, exp, err := s.Create(payloadN(1))
	require.NoError(t, err)
	assert.Len(t, code, codeLen)
	assert.Equal(t, clk.now().Add(codeTTL), exp)

	got, _, err := s.Redeem(code)
	require.NoError(t, err)
	assert.JSONEq(t, string(payloadN(1)), string(got))

	_, _, err = s.Redeem(code)
	assert.ErrorIs(t, err, ErrInvalidCode)
}

// Test 2.
func TestTTL(t *testing.T) {
	t.Run("one nanosecond before expiry succeeds", func(t *testing.T) {
		s, clk := newTestStore()
		code, exp, err := s.Create(payloadN(1))
		require.NoError(t, err)
		clk.set(exp.Add(-time.Nanosecond))
		_, _, err = s.Redeem(code)
		assert.NoError(t, err)
	})
	t.Run("at expiry is invalid", func(t *testing.T) {
		s, clk := newTestStore()
		code, exp, err := s.Create(payloadN(1))
		require.NoError(t, err)
		clk.set(exp)
		_, _, err = s.Redeem(code)
		assert.ErrorIs(t, err, ErrInvalidCode)
	})
	t.Run("expired entries do not count toward capacity", func(t *testing.T) {
		s, clk := newTestStore()
		for i := 0; i < maxLive; i++ {
			_, _, err := s.Create(payloadN(i))
			require.NoError(t, err)
		}
		_, _, err := s.Create(payloadN(99))
		require.ErrorIs(t, err, ErrCapacity)
		clk.advance(codeTTL)
		_, _, err = s.Create(payloadN(99))
		assert.NoError(t, err)
	})
}

// Test 3.
func TestNormalise(t *testing.T) {
	cases := map[string]string{
		"abcd-2345":    "ABCD2345",
		"ABCD 2345":    "ABCD2345",
		"abcd2345":     "ABCD2345",
		" ab-cd 23-45": "ABCD2345",
		"I1L1":         "1111",
		"il":           "11",
		"O0o":          "000",
		"U":            "U",
		"#?":           "#?",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalise(in), "normalise(%q)", in)
	}
}

func TestRedeemNormalisesTheCode(t *testing.T) {
	for _, variant := range []func(string) string{
		func(c string) string { return strings.ToLower(c) },
		func(c string) string { return c[:4] + "-" + c[4:] },
		func(c string) string { return c[:4] + " " + c[4:] },
		func(c string) string { return strings.ToLower(c[:4]) + "-" + c[4:] },
		// The code is 1B0CDEFG: a reader may type I or L for 1 and O for 0.
		func(c string) string { return "iBoCDEFG" },
		func(c string) string { return "LBOCDEFG" },
	} {
		clk := newFakeClock()
		s := newStore(clk.now, func() (string, error) { return "1B0CDEFG", nil })
		code, _, err := s.Create(payloadN(1))
		require.NoError(t, err)
		_, _, err = s.Redeem(variant(code))
		assert.NoError(t, err, "variant %q", variant(code))
	}

	// U is not in the alphabet and is not rewritten: it just misses.
	clk := newFakeClock()
	s := newStore(clk.now, func() (string, error) { return "VBCDEFGH", nil })
	_, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	_, _, err = s.Redeem("UBCDEFGH")
	assert.ErrorIs(t, err, ErrInvalidCode)
}

// Test 4.
func TestUnknownExpiredAndUsedShareOneError(t *testing.T) {
	s, clk := newTestStore()
	used, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	_, _, err = s.Redeem(used)
	require.NoError(t, err)
	expired, _, err := s.Create(payloadN(2))
	require.NoError(t, err)
	clk.advance(codeTTL)

	_, _, errUnknown := s.Redeem(wrongCode)
	_, _, errExpired := s.Redeem(expired)
	_, _, errUsed := s.Redeem(used)
	_, _, errMalformed := s.Redeem("nope")
	for _, e := range []error{errUnknown, errExpired, errUsed, errMalformed} {
		assert.Same(t, ErrInvalidCode, e)
	}
}

// Test 5.
func redeemConcurrently(t *testing.T, n int) (payloads, invalid, limited int) {
	t.Helper()
	s, _ := newTestStore()
	code, _, err := s.Create(payloadN(7))
	require.NoError(t, err)

	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, _, err := s.Redeem(code)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				assert.JSONEq(t, string(payloadN(7)), string(got))
				payloads++
			case errors.Is(err, ErrInvalidCode):
				invalid++
			case errors.Is(err, ErrRateLimited):
				limited++
			default:
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	return
}

func TestConcurrentRedeem64(t *testing.T) {
	payloads, invalid, limited := redeemConcurrently(t, 64)
	assert.Equal(t, 1, payloads, "exactly one redeem gets the payload")
	assert.LessOrEqual(t, invalid, failLimit)
	assert.Equal(t, 64, payloads+invalid+limited)
	// The first redeem through the lock is always the winner, so the losers
	// are 10 invalid then rate-limited.
	assert.Equal(t, failLimit, invalid)
	assert.Equal(t, 64-1-failLimit, limited)
}

func TestConcurrentRedeem11(t *testing.T) {
	payloads, invalid, limited := redeemConcurrently(t, 11)
	assert.Equal(t, 1, payloads)
	assert.Equal(t, 10, invalid)
	assert.Equal(t, 0, limited)
}

// Test 6.
func TestBruteForceWindow(t *testing.T) {
	s, clk := newTestStore()
	code, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	windowStart := clk.now()

	for i := 1; i <= failLimit; i++ {
		_, _, err := s.Redeem(wrongCode)
		require.ErrorIs(t, err, ErrInvalidCode, "failure %d still answers invalid_code", i)
		clk.advance(3 * time.Second) // the window does not slide
	}

	// 11th: even the right code is rate-limited.
	_, retry, err := s.Redeem(code)
	require.ErrorIs(t, err, ErrRateLimited)
	assert.Equal(t, windowStart.Add(failWindow).Sub(clk.now()), retry)
	_, _, err = s.Redeem(wrongCode)
	require.ErrorIs(t, err, ErrRateLimited)

	clk.set(windowStart.Add(failWindow - time.Nanosecond))
	_, retry, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrRateLimited)
	assert.Equal(t, time.Nanosecond, retry)

	// At window end the counter resets and the right code still works.
	clk.set(windowStart.Add(failWindow))
	got, _, err := s.Redeem(code)
	require.NoError(t, err)
	assert.JSONEq(t, string(payloadN(1)), string(got))

	// A fresh window: a wrong code is a plain miss again.
	_, _, err = s.Redeem(wrongCode)
	assert.ErrorIs(t, err, ErrInvalidCode)
}

func TestSuccessDoesNotResetTheCounter(t *testing.T) {
	s, _ := newTestStore()
	code, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	other, _, err := s.Create(payloadN(2))
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, _, err := s.Redeem(wrongCode)
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	_, _, err = s.Redeem(code)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, _, err := s.Redeem(wrongCode)
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	_, _, err = s.Redeem(other)
	assert.ErrorIs(t, err, ErrRateLimited)
}

func TestWindowIsFixedFromTheFirstFailure(t *testing.T) {
	s, clk := newTestStore()
	start := clk.now()
	_, _, err := s.Redeem(wrongCode) // opens the window
	require.ErrorIs(t, err, ErrInvalidCode)

	// Nine more near the end of the window: the tenth failure lands inside it.
	clk.set(start.Add(59 * time.Second))
	for i := 0; i < failLimit-1; i++ {
		_, _, err := s.Redeem(wrongCode)
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	_, retry, err := s.Redeem(wrongCode)
	require.ErrorIs(t, err, ErrRateLimited)
	assert.Equal(t, time.Second, retry)

	// The window closes 60 s after the FIRST failure, not the last.
	clk.set(start.Add(failWindow))
	_, _, err = s.Redeem(wrongCode)
	assert.ErrorIs(t, err, ErrInvalidCode)
}

// Test 7.
func TestWrongCodeLeavesEveryEntryRedeemable(t *testing.T) {
	s, _ := newTestStore()
	var codes []string
	for i := 0; i < 5; i++ {
		c, _, err := s.Create(payloadN(i))
		require.NoError(t, err)
		codes = append(codes, c)
	}
	for _, guess := range []string{wrongCode, "C000000", "C00000000", "", "c000000x"} {
		_, _, err := s.Redeem(guess)
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	for i, c := range codes {
		got, _, err := s.Redeem(c)
		require.NoError(t, err, "code %d", i)
		assert.JSONEq(t, string(payloadN(i)), string(got))
	}
}

// Test 8.
func TestCapacity(t *testing.T) {
	s, _ := newTestStore()
	var first string
	for i := 0; i < maxLive; i++ {
		c, _, err := s.Create(payloadN(i))
		require.NoError(t, err)
		if i == 0 {
			first = c
		}
	}
	_, _, err := s.Create(payloadN(99))
	require.ErrorIs(t, err, ErrCapacity)

	_, _, err = s.Redeem(first)
	require.NoError(t, err)
	_, _, err = s.Create(payloadN(99))
	assert.NoError(t, err)
	_, _, err = s.Create(payloadN(100))
	assert.ErrorIs(t, err, ErrCapacity)
}

// Test 9.
func TestCollisionRetries(t *testing.T) {
	scripted := func(codes ...string) func() (string, error) {
		i := 0
		return func() (string, error) {
			c := codes[i]
			i++
			return c, nil
		}
	}

	t.Run("four collisions then a fresh code", func(t *testing.T) {
		clk := newFakeClock()
		s := newStore(clk.now, scripted("AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "BBBBBBBB"))
		c, _, err := s.Create(payloadN(1))
		require.NoError(t, err)
		require.Equal(t, "AAAAAAAA", c)
		c, _, err = s.Create(payloadN(2))
		require.NoError(t, err)
		assert.Equal(t, "BBBBBBBB", c)
		got, _, err := s.Redeem("AAAAAAAA")
		require.NoError(t, err)
		assert.JSONEq(t, string(payloadN(1)), string(got), "a collision never overwrites")
	})

	t.Run("five collisions is unavailable", func(t *testing.T) {
		clk := newFakeClock()
		s := newStore(clk.now, scripted("AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "AAAAAAAA", "BBBBBBBB"))
		_, _, err := s.Create(payloadN(1))
		require.NoError(t, err)
		_, _, err = s.Create(payloadN(2))
		assert.ErrorIs(t, err, ErrUnavailable)
		got, _, err := s.Redeem("AAAAAAAA")
		require.NoError(t, err)
		assert.JSONEq(t, string(payloadN(1)), string(got))
	})

	t.Run("generator error is unavailable", func(t *testing.T) {
		clk := newFakeClock()
		s := newStore(clk.now, func() (string, error) { return "", errors.New("entropy") })
		_, _, err := s.Create(payloadN(1))
		assert.ErrorIs(t, err, ErrUnavailable)
	})
}

// Test 10.
func TestDefaultGenerator(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		c, err := generateCode()
		require.NoError(t, err)
		require.Len(t, c, codeLen)
		for _, r := range c {
			require.True(t, strings.ContainsRune(alphabet, r), "char %q of %q", r, c)
		}
		seen[c] = true
	}
	assert.Greater(t, len(seen), 1, "not all equal")
}

func TestEncodeCodeMapsEachFiveBitGroup(t *testing.T) {
	assert.Equal(t, "00000000", encodeCode([5]byte{}))
	assert.Equal(t, "ZZZZZZZZ", encodeCode([5]byte{0xff, 0xff, 0xff, 0xff, 0xff}))
	// 0x08 0x86 0x42 0x98 0xE8 = 00001 00010 00011 00100 00101 00110 00111 01000
	assert.Equal(t, "12345678", encodeCode([5]byte{0x08, 0x86, 0x42, 0x98, 0xE8}))
}

func TestClearDropsEveryEntry(t *testing.T) {
	s, _ := newTestStore()
	c, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	s.Clear()
	_, _, err = s.Redeem(c)
	assert.ErrorIs(t, err, ErrInvalidCode)
	for i := 0; i < maxLive; i++ {
		_, _, err := s.Create(payloadN(i))
		require.NoError(t, err)
	}
}

// fakeTimer is one timer handed out by fakeTimers; tests fire it by hand.
type fakeTimer struct {
	d       time.Duration
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

// fakeTimers is the injected afterFunc: it records every timer instead of
// arming a real one.
type fakeTimers struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

func (f *fakeTimers) afterFunc(d time.Duration, fn func()) stopper {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{d: d, fn: fn}
	f.timers = append(f.timers, t)
	return t
}

func (f *fakeTimers) get(i int) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.timers[i]
}

func (f *fakeTimers) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

func newTimedStore(gen func() (string, error)) (*Store, *fakeClock, *fakeTimers) {
	clk := newFakeClock()
	ft := &fakeTimers{}
	return newStoreWith(clk.now, gen, ft.afterFunc), clk, ft
}

func liveEntries(s *Store) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// R1 finding 1: an expired payload leaves memory when its TTL runs out,
// even if no request ever comes to sweep it.
func TestExpiryTimerDropsTheEntryWithoutAnyRequest(t *testing.T) {
	s, _, ft := newTimedStore(seqGen())
	_, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	require.Equal(t, 1, ft.len(), "one timer per entry")
	assert.Equal(t, codeTTL, ft.get(0).d)
	require.Equal(t, 1, liveEntries(s))

	ft.get(0).fn()
	assert.Equal(t, 0, liveEntries(s), "the timer deletes the payload")
}

func TestRedeemStopsTheExpiryTimer(t *testing.T) {
	s, _, ft := newTimedStore(seqGen())
	code, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	_, _, err = s.Redeem(code)
	require.NoError(t, err)
	assert.True(t, ft.get(0).stopped)
}

func TestSweepStopsTheExpiryTimer(t *testing.T) {
	s, clk, ft := newTimedStore(seqGen())
	_, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	clk.advance(codeTTL)
	_, _, err = s.Create(payloadN(2)) // sweeps the first
	require.NoError(t, err)
	assert.True(t, ft.get(0).stopped)
	assert.False(t, ft.get(1).stopped)
}

func TestLateTimerSparesANewEntryUnderTheSameCode(t *testing.T) {
	s, _, ft := newTimedStore(func() (string, error) { return "AAAAAAAA", nil })
	_, _, err := s.Create(payloadN(1))
	require.NoError(t, err)
	_, _, err = s.Redeem("AAAAAAAA")
	require.NoError(t, err)
	_, _, err = s.Create(payloadN(2)) // same code, new entry
	require.NoError(t, err)

	ft.get(0).fn() // the first entry's timer fires late
	require.Equal(t, 1, liveEntries(s), "the new entry survives")
	got, _, err := s.Redeem("AAAAAAAA")
	require.NoError(t, err)
	assert.JSONEq(t, string(payloadN(2)), string(got))
}

// Attacker finding 3: Stop is permanent. It drops every entry and stops its
// timer in one critical section, and afterwards Create is ErrUnavailable
// and Redeem is ErrStopped — neither counted as a failure.
func TestStopIsPermanent(t *testing.T) {
	s, _, ft := newTimedStore(seqGen())
	code, _, err := s.Create(payloadN(1))
	require.NoError(t, err)

	s.Stop()
	assert.Equal(t, 0, liveEntries(s))
	assert.True(t, ft.get(0).stopped)

	_, _, err = s.Create(payloadN(2))
	assert.ErrorIs(t, err, ErrUnavailable)
	for i := 0; i < failLimit+2; i++ {
		_, _, err = s.Redeem(code)
		assert.ErrorIs(t, err, ErrStopped)
	}
	assert.Equal(t, 0, liveEntries(s))
	assert.Equal(t, 1, ft.len(), "no timer armed after Stop")
	s.mu.Lock()
	assert.Zero(t, s.failures, "a redeem after Stop is not a failure")
	s.mu.Unlock()

	s.Stop() // idempotent
}

// Stop racing Create/Redeem (run with -race): once Stop has returned the map
// is empty and stays empty, and every timer ever armed has been stopped —
// so no Create slipped in after Stop.
func TestStopRacesCreateAndRedeem(t *testing.T) {
	for round := 0; round < 20; round++ {
		s, _, ft := newTimedStore(seqGen())
		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 50; i++ {
					if code, _, err := s.Create(payloadN(i)); err == nil && i%2 == 0 {
						_, _, _ = s.Redeem(code)
					}
				}
			}()
		}
		close(start)
		s.Stop()
		assert.Equal(t, 0, liveEntries(s), "empty the moment Stop returns")
		wg.Wait()
		assert.Equal(t, 0, liveEntries(s), "and nothing appears afterwards")
		for i := 0; i < ft.len(); i++ {
			assert.True(t, ft.get(i).stopped, "round %d timer %d armed after Stop or left running", round, i)
		}
	}
}

func TestClearStopsEveryTimer(t *testing.T) {
	s, _, ft := newTimedStore(seqGen())
	for i := 0; i < 3; i++ {
		_, _, err := s.Create(payloadN(i))
		require.NoError(t, err)
	}
	s.Clear()
	require.Equal(t, 3, ft.len())
	for i := 0; i < 3; i++ {
		assert.True(t, ft.get(i).stopped, "timer %d", i)
	}
}
