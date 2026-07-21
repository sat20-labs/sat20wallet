package account

import "testing"

func TestGuardianCapsuleRejectsWrongKey(t *testing.T) {
	privateKey, publicKey, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	shares, err := splitSecret(make([]byte, 32), "00112233445566778899aabbccddeeff", RecoveryMode2Of3, nil)
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := EncryptGuardianShare(shares[2], publicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptGuardianShare(capsule, privateKey); err != nil {
		t.Fatal(err)
	}
	wrongPrivateKey, _, err := GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptGuardianShare(capsule, wrongPrivateKey); err == nil {
		t.Fatal("wrong key decrypted guardian share")
	}
}
