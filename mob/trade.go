package mob

import (
	"github.com/helix-bedrock/entity/behavior"
	"github.com/helix-bedrock/entity/definition"
	"github.com/helix-bedrock/entity/state"
)

// The mob's side of trading: the synced trade data, the trade components and what
// trade_interest shows. The offers themselves are the engine's (the player package's
// trade.go): it gives the mob what each offer wants and sells.

// BDS's actor data keys for trading.
const (
	// DataTradeTarget is the player in the trade window (68), DataTradeTier the tier
	// (101), DataMaxTradeTier the table's last tier (102), DataTradeExperience the
	// experience (103).
	DataTradeTarget     = 68
	DataTradeTier       = 101
	DataMaxTradeTier    = 102
	DataTradeExperience = 103
	// markerTradeResupply is minecraft:trade_resupply.
	markerTradeResupply = "minecraft:trade_resupply"
)

// TradeWant is one offer as trade_interest reads it.
type TradeWant struct {
	BuyA, BuyB, Sell behavior.Item
	Tier             int32
}

// tradeState is the mob's trade state.
type tradeState struct {
	wants []TradeWant
}

// TradeTable is minecraft:economy_trade_table, nil without one. A villager loses it when it
// loses its profession: the engine drops the offers then (the reroll).
func (m *Mob) TradeTable() *state.EconomyTradeTableConfig {
	if m.states == nil {
		return nil
	}
	return m.states.EconomyTradeTable
}

// HasTradeTable reports minecraft:economy_trade_table.
func (m *Mob) HasTradeTable() bool { return m.TradeTable() != nil }

// Resupplying reports minecraft:trade_resupply, which the work goals add on arrival and the
// bed schedule takes away: the engine restocks once each time it appears.
func (m *Mob) Resupplying() bool {
	return m.states != nil && m.states.Markers[markerTradeResupply]
}

// intData is an int actor data value the mob holds, 0 for none.
func (m *Mob) intData(key int) int32 {
	for _, d := range m.data {
		if d.Key == key {
			if v, ok := d.Value.(int32); ok {
				return v
			}
		}
	}
	return 0
}

// TradeTier is the trade tier (actor data 101).
func (m *Mob) TradeTier() int32 { return m.intData(DataTradeTier) }

// TradeExperience is the trade experience (actor data 103).
func (m *Mob) TradeExperience() int32 { return m.intData(DataTradeExperience) }

// MaxTradeTier is the table's last tier (actor data 102).
func (m *Mob) MaxTradeTier() int32 { return m.intData(DataMaxTradeTier) }

// SetTradeTier, SetTradeExperience and SetMaxTradeTier write the trade actor data. Runs: the
// mob's region, outside its think.
func (m *Mob) SetTradeTier(v int32)       { m.setData(DataTradeTier, v) }
func (m *Mob) SetTradeExperience(v int32) { m.setData(DataTradeExperience, v) }
func (m *Mob) SetMaxTradeTier(v int32)    { m.setData(DataMaxTradeTier, v) }

// SetTradingPlayer sets the player in the trade window (NoID for none) and its actor data,
// which trade_with_player and look_at_trading_player read.
func (m *Mob) SetTradingPlayer(id behavior.ID) {
	m.dw.trading = id
	if id == behavior.NoID {
		m.setData(DataTradeTarget, int64(0))
		return
	}
	m.setData(DataTradeTarget, int64(id))
}

// SetTradeWants gives the mob what its offers want and sell. Runs: outside its think.
func (m *Mob) SetTradeWants(w []TradeWant) { m.trade.wants = append(m.trade.wants[:0], w...) }

// --- behavior.TradeInterested ---

// HeldItem is the player's selected item.
func (m *Mob) HeldItem(id behavior.ID) behavior.Item {
	if m.v.r == nil {
		return behavior.Item{}
	}
	p, ok := m.rt.Env.Actor(m.v.r, id).(PlayerInfo)
	if !ok {
		return behavior.Item{}
	}
	var buf [1]string
	held := p.Equipment("main_hand", buf[:0])
	if len(held) == 0 || held[0] == "" {
		return behavior.Item{}
	}
	return behavior.Item{Name: held[0], Count: 1}
}

// CanSeePlayer is the visibility cache's answer: villagers have none, so always.
func (m *Mob) CanSeePlayer(behavior.ID) bool { return true }

// TradeShow is the sell item of the first unlocked offer from index from on that wants the
// held item, and the index after it.
func (m *Mob) TradeShow(held behavior.Item, from int) (behavior.Item, int, bool) {
	tier := m.TradeTier()
	for i := max(from, 0); i < len(m.trade.wants); i++ {
		w := m.trade.wants[i]
		if w.Tier > tier {
			continue
		}
		if sameKind(w.BuyA, held) || sameKind(w.BuyB, held) {
			return w.Sell, i + 1, true
		}
	}
	return behavior.Item{}, 0, false
}

func sameKind(want, held behavior.Item) bool {
	return !want.Empty() && want.Name == held.Name && (want.Aux == 0 || want.Aux == held.Aux)
}

// SetCarriedItem shows the item in the mob's hand: the engine sends it.
func (m *Mob) SetCarriedItem(it behavior.Item) {
	m.SetEquipment(EquipMainhand, it)
	m.do(op{kind: opCarried})
}

// LooksAtMe reports the player looking at the mob; the view test is not ported, so never.
func (m *Mob) LooksAtMe(behavior.ID) bool { return false }

var _ behavior.TradeInterested = (*Mob)(nil)

// ParseFilter reads an entity filter, as trade tables give their items. Runs: boot.
func (rt *Runtime) ParseFilter(raw []byte) (*definition.Filter, error) {
	return rt.parser.ParseFilter(raw)
}

// Test runs a filter on the mob as its subject; nil passes.
func (m *Mob) Test(f *definition.Filter) bool { return f == nil || f.Eval(m.tester(nil)) }
