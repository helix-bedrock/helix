package player

import "github.com/helix-bedrock/protocol/packet"

// The ominous bottle as BDS 1.26.60 runs it (OminousBottleItem: constructor, use,
// useTimeDepleted). A patrol or raid captain drops one when a player
// kills it (loot_tables/entities/pillager_captain.json); its aux value is the amplifier.
const (
	nameOminousBottle = "minecraft:ominous_bottle"
	// ominousUseTicks is the bottle's use duration (BDS's code(32)).
	ominousUseTicks = 32
	// ominousDuration is how long the Bad Omen lasts: 100 minutes.
	ominousDuration = 120000
	// ominousMaxAmplifier is the highest amplifier the bottle gives (level V).
	ominousMaxAmplifier = 4
)

// startOminousBottle starts drinking an ominous bottle, which any player may.
func (p *Player) startOminousBottle(held ItemStack) {
	u := &p.using
	slot := p.inventory.held
	if u.active && u.slot == slot && u.item.Name == held.Name && u.item.Meta == held.Meta {
		return
	}
	*u = usingState{active: true, slot: slot, item: held, left: ominousUseTicks, ominous: true}
	p.sendActorFlags()
}

// drinkOminousBottle is BDS's finished drink: Bad Omen for 100 minutes at the bottle's
// amplifier (its aux, kept to 0..4). A stronger Bad Omen the player has is taken away first,
// so the bottle's level is the one left. Then one bottle is used (not in creative) and the
// ominous_bottle.end_use sound plays at the mouth. Nothing is left behind.
func (p *Player) drinkOminousBottle(held ItemStack) {
	amp := min(max(int32(held.Meta), 0), ominousMaxAmplifier)
	if have, ok := p.Effect(EffectBadOmen); ok && have > amp {
		p.RemoveEffect(EffectBadOmen)
	}
	p.GiveEffect(ActiveEffect{ID: EffectBadOmen, Amplifier: amp, Duration: ominousDuration})
	if !p.creative() {
		p.useHeld()
	}
	if r := p.entity.Region(); r != nil {
		playSound(r, p.blockPos(), soundEvent{name: packet.SoundEventOminousBottleEndUse, pos: p.AttachPos(AttachMouth), extra: -1, entity: "minecraft:player"}, nil)
	}
	p.flush()
}
