package player

import (
	"maps"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/helix-bedrock/helix/world"
	"github.com/helix-bedrock/protocol/packet"
	"github.com/helix-bedrock/world/cube"
)

// Signs and hanging signs as BDS 1.26.60 runs them (SignBlockActor, HangingSignBlockActor,
// SignBlock, HangingSignBlock, SignItem, HangingSignItem). The research note is private.
const (
	signEntityID        = "Sign"
	hangingSignEntityID = "HangingSign"
	// The tags BDS saves a sign in.
	signFrontTag      = "FrontText"
	signBackTag       = "BackText"
	signWaxedTag      = "IsWaxed"
	signLockedTag     = "LockedForEditingBy"
	signTextTag       = "Text"
	signFilteredTag   = "FilteredText"
	signOwnerTag      = "TextOwner"
	signColorTag      = "SignTextColor"
	signGlowTag       = "IgnoreLighting"
	signHideGlowTag   = "HideGlowOutline"
	signPersistTag    = "PersistFormatting"
	signRotationState = "ground_sign_direction"
	signFacingState   = "facing_direction"
	signHangingState  = "hanging"
	signAttachedState = "attached_bit"
	// signRotations is how many ways a standing sign turns.
	signRotations = 16
	// signNoEditor is BDS's empty actor ID in LockedForEditingBy.
	signNoEditor int64 = -1
	// signBlack is the colour a new side's text has, opaque black as ARGB.
	signBlack int32 = -0x1000000
	// The items a sign takes, by what BDS's sign listener compares them with.
	honeycombItem  = "minecraft:honeycomb"
	glowInkSacItem = "minecraft:glow_ink_sac"
	inkSacItem     = "minecraft:ink_sac"
)

// signDyeColors is BDS's dye colour table for sign text, RGB by dye colour index
// (black 0 to white 15); black is pure black.
var signDyeColors = [16]int32{
	0x000000, 0xb02e26, 0x5e7c16, 0x835432, 0x3c44aa, 0x8932b8, 0x169c9c, 0x9d9d97,
	0x474f52, 0xf38baa, 0x80c71f, 0xfed83d, 0x3ab3da, 0xc74ebd, 0xf9801d, 0xf0f0f0,
}

// Each side's Y rotation as BDS's SignBlock and HangingSignBlock give it, by facing_direction
// minus 2 (north, south, west, east).
var (
	wallSignYaw    = [4]float64{360, 180, 270, 90}
	hangingSignYaw = [4]float64{180, 0, 90, -90}
	// hangingFacings is the facing a ceiling hanging sign takes by the quarter its placer
	// looks along.
	hangingFacings = [4]int32{2, 5, 3, 4}
)

// SignSide is one side of a sign: its text, who wrote it, its colour (ARGB) and whether it
// glows.
type SignSide struct {
	Text, FilteredText, Owner string
	Color                     int32
	Glowing, HideGlowOutline  bool
	PersistFormatting         bool
}

// newSignSide is a side as BDS makes one: black text that keeps its formatting.
func newSignSide() SignSide { return SignSide{Color: signBlack, PersistFormatting: true} }

// Sign is what a sign shows on both sides, and whether it is waxed so no one changes it.
type Sign struct {
	Front, Back SignSide
	Waxed       bool
}

// signEntity is a sign's or hanging sign's block entity.
type signEntity struct {
	saved   map[string]any
	hanging bool
	Sign
	// editor is the player who has the sign open, nil for none; BDS keeps its actor ID.
	editor *Player
}

// newSignEntity is a sign with nothing written on it.
func newSignEntity(hanging bool) *signEntity {
	return &signEntity{hanging: hanging, Sign: Sign{Front: newSignSide(), Back: newSignSide()}}
}

// SaveData is what BDS's sign saves: both sides and IsWaxed.
func (e *signEntity) SaveData(pos cube.Pos) map[string]any {
	m := maps.Clone(e.saved)
	if m == nil {
		m = map[string]any{"isMovable": uint8(1)}
	}
	m["id"] = signEntityID
	if e.hanging {
		m["id"] = hangingSignEntityID
	}
	m["x"], m["y"], m["z"] = int32(pos[0]), int32(pos[1]), int32(pos[2]) //nolint:gosec // block coordinates fit int32
	m[signFrontTag] = e.Front.save()
	m[signBackTag] = e.Back.save()
	m[signWaxedTag] = flagByte(e.Waxed)
	return m
}

// NetworkData is the save with who has it open, as BDS sends a sign.
func (e *signEntity) NetworkData(pos cube.Pos) map[string]any {
	m := e.SaveData(pos)
	m[signLockedTag] = e.editorID()
	return m
}

// editorID is BDS's LockedForEditingBy: the editor's unique ID, or none.
func (e *signEntity) editorID() int64 {
	if e.editor == nil {
		return signNoEditor
	}
	return e.editor.uniqueID
}

// editing reports whether someone still in the world has the sign open, as BDS looks the
// editor up; an editor who left no longer holds it.
func (e *signEntity) editing() bool {
	if e.editor != nil && (e.editor.entity == nil || e.editor.entity.Region() == nil) {
		e.editor = nil
	}
	return e.editor != nil
}

// save is a side as BDS saves one.
func (s SignSide) save() map[string]any {
	return map[string]any{
		signGlowTag: flagByte(s.Glowing), signHideGlowTag: flagByte(s.HideGlowOutline),
		signColorTag: s.Color, signPersistTag: flagByte(s.PersistFormatting),
		signOwnerTag: s.Owner, signTextTag: s.Text, signFilteredTag: s.FilteredText,
	}
}

// loadSignSide reads a side as BDS's modern format has it; what is missing keeps a new
// side's value.
func loadSignSide(v any) SignSide {
	s := newSignSide()
	m, ok := v.(map[string]any)
	if !ok {
		return s
	}
	if b, has := m[signGlowTag].(uint8); has {
		s.Glowing = b != 0
	}
	if b, has := m[signHideGlowTag].(uint8); has {
		s.HideGlowOutline = b != 0
	}
	if c, has := m[signColorTag].(int32); has {
		s.Color = c
	}
	if b, has := m[signPersistTag].(uint8); has {
		s.PersistFormatting = b != 0
	}
	s.Owner, _ = m[signOwnerTag].(string)
	s.Text, _ = m[signTextTag].(string)
	s.FilteredText, _ = m[signFilteredTag].(string)
	return s
}

// loadSign brings back a saved sign. Signs saved before 1.20's two sides (Text, Text1..4)
// are not read.
func loadSign(saved map[string]any) *signEntity {
	id, _ := saved["id"].(string)
	e := newSignEntity(id == hangingSignEntityID)
	e.saved = maps.Clone(saved)
	e.Front = loadSignSide(saved[signFrontTag])
	e.Back = loadSignSide(saved[signBackTag])
	if b, ok := saved[signWaxedTag].(uint8); ok {
		e.Waxed = b != 0
	}
	for _, k := range [...]string{signFrontTag, signBackTag, signWaxedTag, signLockedTag} {
		delete(e.saved, k)
	}
	return e
}

// flagByte is a bool as BDS saves one.
func flagByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// isSignBlock reports whether name is a sign or hanging sign block.
func isSignBlock(name string) bool {
	return strings.HasSuffix(name, "standing_sign") || strings.HasSuffix(name, "wall_sign") || isHangingSignName(name)
}

// isHangingSignName reports whether name is a hanging sign, block or item. BDS knows the
// items by their minecraft:hanging_sign tag, which every one of them has.
func isHangingSignName(name string) bool { return strings.HasSuffix(name, "_hanging_sign") }

// isSignItem reports whether an item places a sign.
func isSignItem(name string) bool {
	return strings.HasSuffix(name, "_sign") && !isHangingSignName(name)
}

// signWood is the wood in a sign item's name as its blocks spell it: oak's are the plain
// standing_sign and wall_sign, dark oak's are darkoak_.
func signWood(item string) string {
	w := strings.TrimSuffix(strings.TrimPrefix(item, "minecraft:"), "_sign")
	switch w {
	case "oak":
		return ""
	case "dark_oak":
		return "darkoak_"
	}
	return w + "_"
}

// signBlocks are the standing and wall blocks a sign item places.
func signBlocks(item string) (standing, wall string) {
	w := signWood(item)
	return "minecraft:" + w + "standing_sign", "minecraft:" + w + "wall_sign"
}

// signItemOf is the item a sign block is picked as.
func signItemOf(block string) string {
	if isHangingSignName(block) {
		return block
	}
	w := strings.TrimPrefix(block, "minecraft:")
	w = strings.TrimSuffix(strings.TrimSuffix(w, "standing_sign"), "wall_sign")
	switch w {
	case "":
		w = "oak_"
	case "darkoak_":
		w = "dark_oak_"
	}
	return "minecraft:" + w + "sign"
}

// signRotation is the way a standing sign placed by someone looking along yaw faces, SignItem's
// rule.
func signRotation(yaw float32) int32 {
	return int32(math.Floor(float64((yaw+180)*signRotations/360+0.5))) & (signRotations - 1)
}

// placeSign is SignItem's useOn: never on a block's underside; a replaceable block clicked is
// built over with a standing sign; standing on top, else on the side clicked.
func (p *Player) placeSign(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	f := cube.Face(face)
	if f == cube.FaceDown || !p.inReach(clicked) {
		return false
	}
	blocks := p.s.reg.Blocks
	standing, wall := signBlocks(held.Name)
	pos := clicked.Side(f)
	placed, _ := blocks.Block(standing)
	if c, ok := r.Block(clicked, 0); ok && p.s.game.replaceable(p, r, clicked, c, placed) {
		pos, f = clicked, cube.FaceUp
	}
	var state uint32
	var ok bool
	if f == cube.FaceUp {
		state, ok = blocks.StateID(standing, map[string]any{signRotationState: signRotation(p.entity.Rotation().Yaw)})
	} else {
		state, ok = blocks.StateID(wall, map[string]any{signFacingState: int32(f)})
	}
	if !ok {
		return false
	}
	return p.putSign(r, pos, state, f, held)
}

// placeHangingSign is HangingSignItem's useOn: under a block it hangs from the block, turned
// sixteen ways when attached (sneaking, or the block above has no full underside), else
// four; on any other face it hangs from a side, across the clicked face's axis.
func (p *Player) placeHangingSign(r *world.Region, clicked cube.Pos, face int32, held ItemStack) bool {
	f := cube.Face(face)
	if f < cube.FaceDown || f > cube.FaceEast || !p.inReach(clicked) {
		return false
	}
	pos := clicked.Side(f)
	placed, _ := p.s.reg.Blocks.Block(held.Name)
	if c, ok := r.Block(clicked, 0); ok && p.s.game.replaceable(p, r, clicked, c, placed) {
		pos = clicked
	}
	yaw := p.entity.Rotation().Yaw
	states := map[string]any{signHangingState: uint8(0), signAttachedState: uint8(0), signRotationState: int32(0), signFacingState: int32(0)}
	if f == cube.FaceDown {
		states[signHangingState] = uint8(1)
		quarter := int(math.Floor(float64((yaw+180)*4/360+0.5))) & 3
		states[signFacingState] = hangingFacings[quarter]
		above, ok := r.Block(pos.Side(cube.FaceUp), 0)
		if p.Moving(MoveSneaking) || ok && !p.s.attach.Supports(above, cube.FaceDown) {
			states[signAttachedState] = uint8(1)
			states[signRotationState] = signRotation(yaw)
		}
	} else {
		states[signFacingState] = int32(hangingSideFacing(yaw, f))
	}
	state, ok := p.s.reg.Blocks.StateID(held.Name, states)
	if !ok {
		return false
	}
	return p.putSign(r, pos, state, f, held)
}

// hangingSideFacing is the facing of a sign hung from a side: the first of the directions the
// player looks along that crosses the clicked face's axis, turned to face the player. Clicked
// on top, either axis does, whichever the player looks along more.
func hangingSideFacing(yaw float32, f cube.Face) cube.Face {
	rad := float64(yaw) * math.Pi / 180
	dx, dz := -math.Sin(rad), math.Cos(rad)
	alongX := f == cube.FaceNorth || f == cube.FaceSouth || f == cube.FaceUp && math.Abs(dx) > math.Abs(dz)
	if alongX {
		if dx > 0 {
			return cube.FaceWest
		}
		return cube.FaceEast
	}
	if dz > 0 {
		return cube.FaceNorth
	}
	return cube.FaceSouth
}

// putSign is SignItem's placing once the state is chosen: BDS's checks, the block, the
// sound, the item used up, then the editor opened on the front.
func (p *Player) putSign(r *world.Region, pos cube.Pos, state uint32, f cube.Face, held ItemStack) bool {
	if b, okAt := r.Block(pos, 0); !okAt || !p.s.game.replaceable(p, r, pos, b, state) || !p.s.attach.MayPlace(r, pos, state, f) || p.inside(pos, state) {
		return false
	}
	if g := p.s.game; g.placeEv.Has() {
		e := BlockPlaceEvent{Player: p, Pos: pos, Block: state}
		g.placeEv.fire(p.s.log, &e)
		if e.Cancelled() {
			return false
		}
	}
	if !r.SetBlock(pos, 0, state) {
		return false
	}
	r.UpdateNeighbours(pos)
	playSound(r, pos, p.s.blockSound(packet.SoundEventPlace, blockCenter(pos), state), nil)
	if !p.creative() {
		p.useHeld()
	}
	p.openSign(r, pos, true)
	return true
}

// signAt is the sign block entity at pos.
func signAt(r *world.Region, pos cube.Pos) (*signEntity, bool) {
	be, ok := r.BlockEntity(pos)
	if !ok {
		return nil, false
	}
	e, isSign := be.(*signEntity)
	return e, isSign
}

// SignAt returns what the sign at pos shows. Runs: owner of the region.
func SignAt(r *world.Region, pos cube.Pos) (Sign, bool) {
	e, ok := signAt(r, pos)
	if !ok {
		return Sign{}, false
	}
	return e.Sign, true
}

// SetSignAt changes what the sign at pos shows and shows it to everyone; false if there is
// none. Runs: owner of the region.
func SetSignAt(r *world.Region, pos cube.Pos, s Sign) bool {
	e, ok := signAt(r, pos)
	if !ok {
		return false
	}
	e.Sign = s
	r.SaveBlockEntity(pos)
	r.SendBlockEntity(pos)
	return true
}

// openSign is the player's openSign: the sign is locked to it, sent, then its editor opened.
func (p *Player) openSign(r *world.Region, pos cube.Pos, front bool) {
	e, ok := signAt(r, pos)
	if !ok || e.editing() && e.editor != p {
		return
	}
	e.editor = p
	r.SendBlockEntity(pos)
	p.send(&packet.OpenSign{Position: blockPos(pos), FrontSide: front})
}

// signYaw is the sign's Y rotation in degrees, as SignBlock and HangingSignBlock give it.
func (s *Host) signYaw(block uint32) float64 {
	name, states := s.reg.Blocks.Name(block), s.reg.Blocks.States(block)
	rotation, _ := states[signRotationState].(int32)
	facing, _ := states[signFacingState].(int32)
	attached, _ := states[signAttachedState].(uint8)
	switch {
	case isHangingSignName(name) && attached == 0:
		if facing < 2 || facing > 5 {
			return 0
		}
		return hangingSignYaw[facing-2]
	case strings.HasSuffix(name, "wall_sign"):
		if facing < 2 || facing > 5 {
			return 0
		}
		return wallSignYaw[facing-2] + plainSignTurn
	case isHangingSignName(name):
		return math.Mod(float64(rotation)*360/signRotations+180, 360)
	}
	return math.Mod(float64(rotation)*360/signRotations+180+plainSignTurn, 360)
}

// plainSignTurn turns standing and wall signs from the rotation read in BDS's SignBlock to
// the one the side rule needs: tested in game, BDS's numbers as read gave the back side to
// a player in front of a wall sign, while hanging signs, read the same way, were right.
const plainSignTurn = 180

// signBackFaces reports whether the player sees the sign's back, BDS's rule: the player's
// side of the line through the sign's middle along its rotation. Helix takes the block's
// middle where BDS takes the middle of the sign's shape.
func (p *Player) signBackFaces(pos cube.Pos, block uint32) bool {
	rad := p.s.signYaw(block) * math.Pi / 180
	at := p.entity.Position()
	cx, cz := float64(pos[0])+0.5, float64(pos[2])+0.5
	return (float64(at[0])-cx)*-math.Sin(rad)+(float64(at[2])-cz)*math.Cos(rad) < 0
}

// side is the side of e a player sees.
func (e *signEntity) side(back bool) *SignSide {
	if back {
		return &e.Back
	}
	return &e.Front
}

// useSign is SignBlock's use listener: a waxed sign only plays its sound; honeycomb waxes it,
// a glow ink sac makes the side seen glow, an ink sac takes the glow away, a dye colours the
// text; anything else, or one of these with nothing to change, opens the side seen for
// editing. False leaves the click to the held item.
func (p *Player) useSign(r *world.Region, u *BlockUse) bool {
	e, ok := signAt(r, u.Pos)
	if !ok {
		return false
	}
	if e.hanging && isHangingSignName(u.Item.Name) {
		return false
	}
	if p.mode() == GameModeAdventure || e.editing() {
		return false
	}
	if e.Waxed {
		playSound(r, u.Pos, soundEvent{name: packet.SoundEventWaxedSignInteractFail, pos: blockVec(u.Pos), extra: -1}, nil)
		return true
	}
	back := p.signBackFaces(u.Pos, u.Block)
	side := e.side(back)
	switch held := u.Item.Name; {
	case held == honeycombItem:
		e.Waxed = true
		p.signItemUsed(r, u.Pos, packet.LevelEventWaxOn, blockCenter(u.Pos))
		return true
	case held == glowInkSacItem:
		if side.Glowing || side.Text == "" {
			break
		}
		side.Glowing, side.HideGlowOutline = true, false
		p.signItemUsed(r, u.Pos, packet.LevelEventSoundInkSacUsed, blockVec(u.Pos))
		return true
	case held == inkSacItem:
		if !side.Glowing || side.Text == "" {
			break
		}
		side.Glowing, side.HideGlowOutline = false, false
		p.signItemUsed(r, u.Pos, packet.LevelEventSoundInkSacUsed, blockVec(u.Pos))
		return true
	default:
		dye, isDye := p.s.banners().Dyes[held]
		if !isDye || int(dye) >= len(signDyeColors) {
			break
		}
		color := signBlack | signDyeColors[dye]
		if side.Color == color || side.Text == "" {
			break
		}
		side.Color, side.HideGlowOutline = color, false
		p.signItemUsed(r, u.Pos, packet.LevelEventSoundDyeUsed, blockVec(u.Pos))
		return true
	}
	p.openSign(r, u.Pos, !back)
	return true
}

// signItemUsed finishes a change made with an item: the item used up outside creative, the
// sign saved and shown, and BDS's level event.
func (p *Player) signItemUsed(r *world.Region, pos cube.Pos, event int32, at mgl32.Vec3) {
	if !p.creative() {
		p.useHeld()
	}
	r.SaveBlockEntity(pos)
	r.SendBlockEntity(pos)
	levelEvent(r, pos, at, event, 0)
}

// handleBlockActorData is a client sending a sign it edited, BDS's onUpdatePacket: each side
// whose text changed is taken whole and owned by the editor, then the sign is unlocked. BDS's
// packet handler before it was not read; Helix takes the edit only from the player editing.
func handleBlockActorData(p *Player, pk *packet.BlockActorData) {
	if p.entity == nil {
		return
	}
	r := p.entity.Region()
	if r == nil {
		return
	}
	pos := toCube(pk.Position)
	e, ok := signAt(r, pos)
	if !ok || !e.editing() || e.editor != p {
		return
	}
	editSignSide(&e.Front, pk.NBTData[signFrontTag], p.XUID())
	editSignSide(&e.Back, pk.NBTData[signBackTag], p.XUID())
	e.editor = nil
	r.SaveBlockEntity(pos)
	r.SendBlockEntity(pos)
}

// editSignSide takes a side the client sent in place of side when its text changed, owned
// by owner, as BDS does.
func editSignSide(side *SignSide, sent any, owner string) {
	if _, ok := sent.(map[string]any); !ok {
		return
	}
	next := loadSignSide(sent)
	if next.Text == side.Text {
		return
	}
	next.Owner = owner
	*side = next
}
