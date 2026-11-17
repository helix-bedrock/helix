package player

import "github.com/helix-bedrock/entity/behavior"

// ItemDamaged implements mob.DamageHooks: the stack has taken damage.
func (m *Mob) ItemDamaged(it behavior.Item) bool { return realStack(it).Damage() > 0 }
