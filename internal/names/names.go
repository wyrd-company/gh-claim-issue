// Package names produces random two-word agent identifiers from a curated
// list of 50 adjectives and 50 nouns drawn from programmer, fantasy,
// mythology, sci-fi, and cyberpunk vocabularies (2,500 unique pairings).
//
// Generate maintains a rolling history of the most recent HistorySize
// names so the same identifier won't be handed out twice within that
// window, even across processes (the history file is mutex'd via flock).
package names

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var adjectives = [...]string{
	"quantum", "recursive", "sentient", "holographic", "encrypted",
	"asynchronous", "polymorphic", "monadic", "idempotent", "lambda",
	"binary", "fractal", "parallel", "distributed", "neural",
	"semantic", "atomic", "immutable", "compiled", "hexadecimal",
	"cryptographic", "virtual", "ephemeral", "persistent", "deterministic",
	"stochastic", "modular", "eldritch", "arcane", "glitching",
	"overclocked", "jacked", "wired", "chromed", "neon",
	"augmented", "synthetic", "cybernetic", "nanoscale", "plasma",
	"photonic", "magnetic", "entropic", "paradoxical", "transcendent",
	"runic", "mythic", "void", "spectral", "obsidian",
}

var nouns = [...]string{
	"golem", "oracle", "lich", "kraken", "phoenix",
	"dragon", "valkyrie", "basilisk", "sphinx", "chimera",
	"leviathan", "gorgon", "manticore", "hydra", "wyvern",
	"druid", "paladin", "archon", "daemon", "wraith",
	"automaton", "replicant", "netrunner", "samurai", "ronin",
	"decker", "ripper", "mecha", "android", "cyborg",
	"drone", "sentinel", "warden", "beacon", "kernel",
	"sigil", "glyph", "codex", "talisman", "monolith",
	"nebula", "quasar", "pulsar", "singularity", "specter",
	"banshee", "revenant", "shoggoth", "icebreaker", "wetware",
}

func pick(n int) int {
	i, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(fmt.Sprintf("names: crypto/rand failed: %v", err))
	}
	return int(i.Int64())
}
