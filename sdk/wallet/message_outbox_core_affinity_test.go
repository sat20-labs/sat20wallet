package wallet

import (
	"strings"
	"testing"
)

func TestMessageOutboxesFailClosedOnCoreNodeChange(t *testing.T) {
	client := newRGB11MessageNodeClient(newRGB11MemoryDKVSHTTP())
	manager := &Manager{}
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())

	direct := &accountMessageOutboxRecord{
		Version: accountMessageOutboxVersion, ApplicationID: "direct-affinity",
		SourceCoreNode: "different-core-node",
	}
	if _, err := manager.sendPersistedAccountMessage(direct); err == nil || !strings.Contains(err.Error(), "different CoreNode") {
		t.Fatalf("Direct outbox switch err=%v", err)
	}

	topic := &topicMessageOutboxRecord{
		Version: topicMessageOutboxVersion, ApplicationID: "topic-affinity",
		SourceCoreNode: "different-core-node",
	}
	if _, err := manager.sendPersistedTopicMessage(topic); err == nil || !strings.Contains(err.Error(), "different CoreNode") {
		t.Fatalf("Topic outbox switch err=%v", err)
	}
}
