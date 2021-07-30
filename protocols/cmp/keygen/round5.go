package keygen

import (
	"errors"

	"github.com/taurusgroup/cmp-ecdsa/internal/proto"
	"github.com/taurusgroup/cmp-ecdsa/internal/round"
	"github.com/taurusgroup/cmp-ecdsa/pkg/math/curve"
	"github.com/taurusgroup/cmp-ecdsa/pkg/math/polynomial"
	"github.com/taurusgroup/cmp-ecdsa/pkg/party"
	"github.com/taurusgroup/cmp-ecdsa/pkg/protocol/message"
	"github.com/taurusgroup/cmp-ecdsa/pkg/protocol/types"
	zkmod "github.com/taurusgroup/cmp-ecdsa/pkg/zk/mod"
	zkprm "github.com/taurusgroup/cmp-ecdsa/pkg/zk/prm"
)

var _ round.Round = (*round5)(nil)

type round5 struct {
	*round4

	// RID = ⊕ⱼ RIDⱼ
	// Random ID generated by taking the XOR of all ridᵢ
	RID RID
}

// ProcessMessage implements round.Round.
//
// - decrypt share
// - verify VSS.
func (r *round5) ProcessMessage(j party.ID, content message.Content) error {
	body := content.(*Keygen5)
	// decrypt share
	DecryptedShare, err := r.PaillierSecret.Dec(body.Share)
	if err != nil {
		return err
	}
	Share := curve.NewScalarInt(DecryptedShare)
	if DecryptedShare.Eq(Share.Int()) != 1 {
		return ErrRound5Decrypt
	}

	// verify share with VSS
	ExpectedPublicShare := r.VSSPolynomials[j].Evaluate(r.SelfID().Scalar()) // Fⱼ(i)
	PublicShare := curve.NewIdentityPoint().ScalarBaseMult(Share)
	// X == Fⱼ(i)
	if !PublicShare.Equal(ExpectedPublicShare) {
		return ErrRound5VSS
	}

	// verify zkmod
	if !body.Mod.Verify(r.HashForID(j), zkmod.Public{N: r.N[j]}) {
		return ErrRound5ZKMod
	}

	// verify zkprm
	if !body.Prm.Verify(r.HashForID(j), zkprm.Public{N: r.N[j], S: r.S[j], T: r.T[j]}) {
		return ErrRound5ZKPrm
	}

	r.ShareReceived[j] = Share
	return nil
}

// Finalize implements round.Round
//
// - sum of all received shares
// - compute group public key and individual public keys
// - recompute config SSID
// - validate Config
// - write new ssid hash to old hash state
// - create proof of knowledge of secret.
func (r *round5) Finalize(out chan<- *message.Message) (round.Round, error) {
	// add all shares to our secret
	UpdatedSecretECDSA := curve.NewScalar().Set(r.PreviousSecretECDSA)
	for _, j := range r.PartyIDs() {
		UpdatedSecretECDSA.Add(UpdatedSecretECDSA, r.ShareReceived[j])
	}

	// [F₁(X), …, Fₙ(X)]
	ShamirPublicPolynomials := make([]*polynomial.Exponent, 0, len(r.VSSPolynomials))
	for _, VSSPolynomial := range r.VSSPolynomials {
		ShamirPublicPolynomials = append(ShamirPublicPolynomials, VSSPolynomial)
	}

	// ShamirPublicPolynomial = F(X) = ∑Fⱼ(X)
	ShamirPublicPolynomial, err := polynomial.Sum(ShamirPublicPolynomials)
	if err != nil {
		return nil, err
	}

	// compute the new public key share Xⱼ = F(j) (+X'ⱼ if doing a refresh)
	PublicData := make(map[party.ID]*Public, len(r.PartyIDs()))
	for _, j := range r.PartyIDs() {
		PublicECDSAShare := ShamirPublicPolynomial.Evaluate(j.Scalar())
		PublicECDSAShare.Add(PublicECDSAShare, r.PreviousPublicSharesECDSA[j])
		PublicData[j] = &Public{
			ECDSA: PublicECDSAShare,
			N:     r.N[j],
			S:     r.S[j],
			T:     r.T[j],
		}
	}

	UpdatedConfig := &Config{
		Threshold: uint32(r.Threshold),
		Public:    PublicData,
		RID:       r.RID.Copy(),
		Secret: &Secret{
			ID:    r.SelfID(),
			ECDSA: UpdatedSecretECDSA,
			P:     &proto.NatMarshaller{Nat: r.PaillierSecret.P()},
			Q:     &proto.NatMarshaller{Nat: r.PaillierSecret.Q()},
		},
	}

	// write new ssid to hash, to bind the Schnorr proof to this new config
	// Write SSID, selfID to temporary hash
	h := r.Hash()
	_, _ = h.WriteAny(UpdatedConfig, r.SelfID())

	proof := r.SchnorrRand.Prove(h, PublicData[r.SelfID()].ECDSA, UpdatedSecretECDSA)

	// send to all
	msg := r.MarshalMessage(&KeygenOutput{SchnorrResponse: proof}, r.OtherPartyIDs()...)
	if err = r.SendMessage(msg, out); err != nil {
		return r, err
	}

	r.UpdateHashState(UpdatedConfig)
	return &output{
		round5:        r,
		UpdatedConfig: UpdatedConfig,
	}, nil
}

// MessageContent implements round.Round.
func (r *round5) MessageContent() message.Content { return &Keygen5{} }

// Validate implements message.Content.
func (m *Keygen5) Validate() error {
	if m == nil {
		return errors.New("keygen.round3: message is nil")
	}
	if m.Mod == nil {
		return errors.New("keygen.round3: zkmod proof is nil")
	}
	if m.Prm == nil {
		return errors.New("keygen.round3: zkprm proof is nil")
	}
	if m.Share == nil {
		return errors.New("keygen.round3: Share proof is nil")
	}
	return nil
}

// RoundNumber implements message.Content.
func (m *Keygen5) RoundNumber() types.RoundNumber { return 5 }
