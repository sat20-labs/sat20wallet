package wallet

import "testing"

// Genuine old fixture bytes and all their existing assertions are unchanged.
// An empty-but-nonnil RGB recovery slot is still a violation of old custody.
func TestLegacyGobRGBRecoveryFieldMustRemainNil(t *testing.T) {
	for _, name := range []string{"channel_plain.gob", "channel_ordx.gob"} {
		for _, side := range []string{"local", "remote"} {
			t.Run(name+"/"+side, func(t *testing.T) {
				raw, _ := legacyGobReadFixture(t, name)
				var channel ChannelInDB
				if err := DecodeFromBytes(raw, &channel); err != nil {
					t.Fatal(err)
				}
				if err := legacyFixtureRGBRecoveryIsolation(&channel); err != nil {
					t.Fatalf("genuine legacy nil control failed: %v", err)
				}
				commitment := channel.LocalCommitment
				if side == "remote" {
					commitment = channel.RemoteCommitment
				}
				if commitment == nil {
					t.Fatal("genuine old commitment is unavailable")
				}
				commitment.RGBClientRecovery = &CommitmentTx{}
				if err := legacyFixtureRGBRecoveryIsolation(&channel); err == nil {
					t.Fatal("old-asset isolation silently ignored a nonnil RGB-only field")
				}
				if !legacyFixtureNativeField("ChannelCommitmentV1", "RGBClientRecovery") {
					t.Fatal("additive RGB field was not explicitly registered in the semantic schema boundary")
				}
			})
		}
	}
}
