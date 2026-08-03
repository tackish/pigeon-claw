package discord

import "testing"

// TestChannelPolicyFiltersForeignChannel is the regression test for the
// bot appearing to reply twice. Several instances can share one Discord
// token while being configured for different channels; the gateway hands
// every event to all of them, so the channel filter is what keeps each in
// its own channels. Slash commands used to skip it, which is why /status
// and /status drew two replies while ordinary messages drew one.
func TestChannelPolicyFiltersForeignChannel(t *testing.T) {
	h := NewHandler(nil, []string{"chan-mine"}, nil, "")

	if _, _, serves := h.channelPolicy("chan-mine"); !serves {
		t.Fatal("must serve a channel in its allow list")
	}
	if _, _, serves := h.channelPolicy("chan-someone-elses"); serves {
		t.Fatal("must not serve a channel it is not configured for — " +
			"that is what makes a foreign instance answer here")
	}
}

func TestChannelPolicyMentionChannelIsServed(t *testing.T) {
	h := NewHandler(nil, nil, []string{"chan-mention"}, "")

	_, mentionOnly, serves := h.channelPolicy("chan-mention")
	if !serves {
		t.Fatal("a mention channel must be served")
	}
	if !mentionOnly {
		t.Fatal("a mention channel must be reported as mention-only")
	}

	if _, _, serves := h.channelPolicy("other"); serves {
		t.Fatal("a mention list still filters other channels")
	}
}

// TestChannelPolicyNoFilterServesEverything preserves the unconfigured
// default: with no lists set, the bot answers everywhere.
func TestChannelPolicyNoFilterServesEverything(t *testing.T) {
	h := NewHandler(nil, nil, nil, "")

	if _, _, serves := h.channelPolicy("anything"); !serves {
		t.Fatal("with no channel lists configured, every channel is served")
	}
}

// TestChannelPolicyFollowsReload keeps the filter correct after a SIGHUP
// config reload moves the bot to different channels.
func TestChannelPolicyFollowsReload(t *testing.T) {
	h := NewHandler(nil, []string{"old-chan"}, nil, "")

	h.UpdateAllowedChannels([]string{"new-chan"})

	if _, _, serves := h.channelPolicy("old-chan"); serves {
		t.Fatal("a channel dropped from the config must stop being served")
	}
	if _, _, serves := h.channelPolicy("new-chan"); !serves {
		t.Fatal("a channel added by reload must start being served")
	}
}
