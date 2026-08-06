// Package dkvs contains wallet-independent DKVS protocol building blocks.
//
// It owns deterministic record signing helpers, Blob and application payload
// codecs, connected-node FREE_LOCAL policy normalization, stable typed errors,
// and confirmed-replica persistence. It must not import the parent wallet
// package or know about account management, RGB11, UI state, sync workers, or
// endpoint selection.
//
// The parent wallet package owns dkvsManager. That manager composes these
// primitives with wallet keys, endpoint routing, readiness, outbox retry and
// domain-specific adapters. Compatibility facades in the parent package may
// delegate here, but must not duplicate the low-level implementation.
package dkvs
