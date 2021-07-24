package sign

import (
	"crypto/rand"
	mrand "math/rand"
	"reflect"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taurusgroup/cmp-ecdsa/pkg/message"
	"github.com/taurusgroup/cmp-ecdsa/pkg/params"
	"github.com/taurusgroup/cmp-ecdsa/pkg/party"
	"github.com/taurusgroup/cmp-ecdsa/pkg/round"
	"github.com/taurusgroup/cmp-ecdsa/protocols/cmp/keygen"
	"golang.org/x/crypto/sha3"
)

var roundTypes = []reflect.Type{
	reflect.TypeOf(&round1{}),
	reflect.TypeOf(&round2{}),
	reflect.TypeOf(&round3{}),
	reflect.TypeOf(&round4{}),
	reflect.TypeOf(&output{}),
}

func processRound(t *testing.T, rounds map[party.ID]round.Round, expectedRoundType reflect.Type) {
	N := len(rounds)
	t.Logf("starting round %v", expectedRoundType)
	// get the second set of  messages
	out := make(chan *message.Message, N*N)
	for id, r := range rounds {
		assert.EqualValues(t, expectedRoundType, reflect.TypeOf(r))
		newRound, err := r.Finalize(out)
		require.NoError(t, err, "failed to generate messages")

		if newRound != nil {
			rounds[id] = newRound
		}
	}
	close(out)

	for msg := range out {
		msgBytes, err := proto.Marshal(msg)
		require.NoError(t, err, "failed to marshal message")
		for idJ, r := range rounds {
			var m message.Message
			require.NoError(t, proto.Unmarshal(msgBytes, &m), "failed to unmarshal message")
			if m.From == idJ {
				continue
			}
			if len(m.To) == 0 || party.IDSlice(m.To).Contains(idJ) {
				content := r.MessageContent()
				err = msg.UnmarshalContent(content)
				require.NoError(t, err)
				require.NoError(t, r.ProcessMessage(msg.From, content))
			}

		}
	}

	t.Logf("round %v done", expectedRoundType)
}

func TestRound(t *testing.T) {
	N := 2
	T := 1

	rid := make([]byte, params.SecBytes)
	_, _ = rand.Read(rid)

	t.Log("generating configs")
	configs := keygen.FakeData(N, T, mrand.New(mrand.NewSource(1)))
	partyIDs := make([]party.ID, 0, T+1)
	for id := range configs {
		partyIDs = append(partyIDs, id)
		if len(partyIDs) == T+1 {
			break
		}
	}
	t.Log("done generating configs")

	messageToSign := []byte("hello")
	messageHash := make([]byte, 64)
	sha3.ShakeSum128(messageHash, messageToSign)

	rounds := make(map[party.ID]round.Round, N)
	var err error
	for _, partyID := range partyIDs {
		c := configs[partyID]
		rounds[partyID], _, err = StartSign(c, partyIDs, messageHash)()
		require.NoError(t, err, "round creation should not result in an error")
	}

	for _, roundType := range roundTypes {
		processRound(t, rounds, roundType)
	}
}
