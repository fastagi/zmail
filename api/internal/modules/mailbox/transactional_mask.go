package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/transactional"

const (
	maskedSecret    = transactional.MaskedSecret
	maskedURLPath   = transactional.MaskedURLPath
	maskedLocalPart = transactional.MaskedLocalPart
)

func maskSecret(value string) string {
	return transactional.MaskSecret(value)
}

func maskRecipient(address string) string {
	return transactional.MaskRecipient(address)
}

func transactionalFingerprintKey(secret string) []byte {
	return transactional.FingerprintKey(secret)
}

func transactionalFingerprint(key []byte, templateKey, to string, variables map[string]string) []byte {
	return transactional.Fingerprint(key, templateKey, to, variables)
}

func simulatorOutcome(to, simulatorDomain string) (outcome string, simulated bool, ok bool) {
	return transactional.SimulatorOutcome(to, simulatorDomain)
}
