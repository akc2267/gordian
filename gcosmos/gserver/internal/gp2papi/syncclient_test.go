package gp2papi_test

import (
	"bytes"
	"context"
	"testing"

	"cosmossdk.io/core/transaction"
	"github.com/rollchains/gordian/gcosmos/gserver/gservertest"
	"github.com/rollchains/gordian/gcosmos/gserver/internal/gp2papi"
	"github.com/rollchains/gordian/gcosmos/gserver/internal/gsbd"
	"github.com/rollchains/gordian/internal/gtest"
	"github.com/rollchains/gordian/tm/tmconsensus"
	"github.com/rollchains/gordian/tm/tmconsensus/tmconsensustest"
	"github.com/rollchains/gordian/tm/tmengine/tmelink"
	"github.com/stretchr/testify/require"
)

func TestSyncClient_fullBlock_zeroData(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dhfx := NewFixture(t, ctx)

	fx := tmconsensustest.NewStandardFixture(4)
	dataID := gsbd.DataID(1, 0, 0, nil) // Zero data ID
	ph1 := fx.NextProposedHeader([]byte(dataID), 0)
	fx.SignProposal(ctx, &ph1, 0)

	precommitProofs := fx.PrecommitProofMap(ctx, 1, 0, map[string][]int{
		string(ph1.Header.Hash): {0, 1, 2},
		"":                      {3},
	})
	fx.CommitBlock(ph1.Header, []byte("app_state_1"), 0, precommitProofs)
	nextPH := fx.NextProposedHeader([]byte("whatever"), 0)

	require.NoError(t, dhfx.HeaderStore.SaveHeader(ctx, tmconsensus.CommittedHeader{
		Header: ph1.Header,
		Proof:  nextPH.Header.PrevCommitProof,
	}))

	// TODO: we need a mock version of transactions and decoders
	// in order to actually test decoding.
	var txDecoder transaction.Codec[transaction.Tx]

	rhCh := make(chan tmelink.ReplayedHeaderRequest)
	sc := gp2papi.NewSyncClient(
		ctx,
		gtest.NewLogger(t).With("sys", "syncclient"),
		gp2papi.SyncClientConfig{
			Host:               dhfx.P2PClientConn.Host().Libp2pHost(),
			Unmarshaler:        dhfx.Codec,
			TxDecoder:          txDecoder,
			RequestCache:       dhfx.Cache,
			ReplayedHeadersOut: rhCh,
		},
	)
	defer sc.Wait()
	defer cancel()

	// Resume request before any peers are added,
	// just to exercise the behavior when blocked on lack of peers.
	require.True(t, sc.ResumeFetching(ctx, 1, 2)) // Fetch Height 1 and stop at 2.

	// Now add the host peer.
	require.True(t, sc.AddPeer(ctx, dhfx.P2PHostConn.Host().Libp2pHost().ID()))

	// Get the response.
	replayReq := gtest.ReceiveSoon(t, rhCh)
	require.Equal(t, ph1.Header, replayReq.Header)
	require.Zero(t, replayReq.Proof.Round)
	require.Equal(t, nextPH.Header.PrevCommitProof.PubKeyHash, replayReq.Proof.PubKeyHash)

	// Signal back to the client that the replay was good.
	gtest.SendSoon(t, replayReq.Resp, tmelink.ReplayedHeaderResponse{})

	// There is no entry in the request cache, for zero data.
	_, ok := dhfx.Cache.Get(dataID)
	require.False(t, ok)
}

func TestSyncClient_fullBlock_withData_correct(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dhfx := NewFixture(t, ctx)

	fx := tmconsensustest.NewStandardFixture(4)
	tx := gservertest.NewHashOnlyTransaction(1)
	txs := []transaction.Tx{tx}

	var buf bytes.Buffer
	sz, err := gsbd.EncodeBlockData(&buf, txs)
	require.NoError(t, err)

	dataID := gsbd.DataID(1, 0, uint32(sz), txs)
	require.NoError(t, dhfx.BlockDataStore.SaveBlockData(ctx, 1, dataID, buf.Bytes()))

	ph1 := fx.NextProposedHeader([]byte(dataID), 0)
	fx.SignProposal(ctx, &ph1, 0)

	precommitProofs := fx.PrecommitProofMap(ctx, 1, 0, map[string][]int{
		string(ph1.Header.Hash): {0, 1, 2},
		"":                      {3},
	})
	fx.CommitBlock(ph1.Header, []byte("app_state_1"), 0, precommitProofs)
	nextPH := fx.NextProposedHeader([]byte("whatever"), 0)

	require.NoError(t, dhfx.HeaderStore.SaveHeader(ctx, tmconsensus.CommittedHeader{
		Header: ph1.Header,
		Proof:  nextPH.Header.PrevCommitProof,
	}))

	rhCh := make(chan tmelink.ReplayedHeaderRequest)
	sc := gp2papi.NewSyncClient(
		ctx,
		gtest.NewLogger(t).With("sys", "syncclient"),
		gp2papi.SyncClientConfig{
			Host:               dhfx.P2PClientConn.Host().Libp2pHost(),
			Unmarshaler:        dhfx.Codec,
			TxDecoder:          gservertest.HashOnlyTransactionDecoder{},
			RequestCache:       dhfx.Cache,
			ReplayedHeadersOut: rhCh,
		},
	)
	defer sc.Wait()
	defer cancel()

	// Resume request before any peers are added,
	// just to exercise the behavior when blocked on lack of peers.
	require.True(t, sc.ResumeFetching(ctx, 1, 2)) // Fetch Height 1 and stop at 2.

	// Now add the host peer.
	require.True(t, sc.AddPeer(ctx, dhfx.P2PHostConn.Host().Libp2pHost().ID()))

	// Get the response.
	replayReq := gtest.ReceiveSoon(t, rhCh)
	require.Equal(t, ph1.Header, replayReq.Header)
	require.Zero(t, replayReq.Proof.Round)
	require.Equal(t, nextPH.Header.PrevCommitProof.PubKeyHash, replayReq.Proof.PubKeyHash)

	// Signal back to the client that the replay was good.
	gtest.SendSoon(t, replayReq.Resp, tmelink.ReplayedHeaderResponse{})

	// And since the replay was good,
	// we have a completed block data request in the cache.
	r, ok := dhfx.Cache.Get(dataID)
	require.True(t, ok)

	_ = gtest.IsSending(t, r.Ready)
	require.Equal(t, txs, r.Transactions)
	require.Equal(t, buf.Bytes(), r.EncodedTransactions)
}

func TestSyncClient_fullBlock_withData_badHash(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dhfx := NewFixture(t, ctx)

	fx := tmconsensustest.NewStandardFixture(4)
	tx1 := gservertest.NewHashOnlyTransaction(1)
	tx2 := gservertest.NewHashOnlyTransaction(2)

	// We're going to calculate the hash with 1-2, but send 2-1.
	// The data size should be the same, but the hash comparison will fail.
	txs12 := []transaction.Tx{tx1, tx2}
	txs21 := []transaction.Tx{tx2, tx1}

	var buf bytes.Buffer
	sz, err := gsbd.EncodeBlockData(&buf, txs21)
	require.NoError(t, err)

	dataID := gsbd.DataID(1, 0, uint32(sz), txs12)
	require.NoError(t, dhfx.BlockDataStore.SaveBlockData(ctx, 1, dataID, buf.Bytes()))

	ph1 := fx.NextProposedHeader([]byte(dataID), 0)
	fx.SignProposal(ctx, &ph1, 0)

	precommitProofs := fx.PrecommitProofMap(ctx, 1, 0, map[string][]int{
		string(ph1.Header.Hash): {0, 1, 2, 3},
	})
	fx.CommitBlock(ph1.Header, []byte("app_state_1"), 0, precommitProofs)
	nextPH := fx.NextProposedHeader([]byte("whatever"), 0)

	require.NoError(t, dhfx.HeaderStore.SaveHeader(ctx, tmconsensus.CommittedHeader{
		Header: ph1.Header,
		Proof:  nextPH.Header.PrevCommitProof,
	}))

	rhCh := make(chan tmelink.ReplayedHeaderRequest)
	sc := gp2papi.NewSyncClient(
		ctx,
		gtest.NewLogger(t).With("sys", "syncclient"),
		gp2papi.SyncClientConfig{
			Host:               dhfx.P2PClientConn.Host().Libp2pHost(),
			Unmarshaler:        dhfx.Codec,
			TxDecoder:          gservertest.HashOnlyTransactionDecoder{},
			RequestCache:       dhfx.Cache,
			ReplayedHeadersOut: rhCh,
		},
	)
	defer sc.Wait()
	defer cancel()

	// Add the host peer first.
	require.True(t, sc.AddPeer(ctx, dhfx.P2PHostConn.Host().Libp2pHost().ID()))

	// Now fetch height 1.
	require.True(t, sc.ResumeFetching(ctx, 1, 2)) // Fetch Height 1 and stop at 2.

	// We don't receive a replayed header
	// on account of the hash mismatch,
	// and we don't have any alternate peers who are hosting the data either.
	gtest.NotSendingSoon(t, rhCh)

	// Nothing in the request cache since we never got valid data.
	_, ok := dhfx.Cache.Get(dataID)
	require.False(t, ok)
}