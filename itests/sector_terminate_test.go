//stm: #integration
package itests

import (
	"context"
	"testing"
	"time"

	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/chain/types"
	sealing "github.com/filecoin-project/lotus/extern/storage-sealing"
	"github.com/filecoin-project/lotus/itests/kit"
	"github.com/stretchr/testify/require"
)

func TestTerminate(t *testing.T) {
	//stm: @BLOCKCHAIN_SYNCER_LOAD_GENESIS_001, @BLOCKCHAIN_SYNCER_FETCH_TIPSET_001,
	//stm: @BLOCKCHAIN_SYNCER_START_001, @BLOCKCHAIN_SYNCER_SYNC_001, @BLOCKCHAIN_BEACON_VALIDATE_BLOCK_01
	//stm: @BLOCKCHAIN_SYNCER_COLLECT_CHAIN_001, @BLOCKCHAIN_SYNCER_COLLECT_HEADERS_001, @BLOCKCHAIN_SYNCER_VALIDATE_TIPSET_001
	//stm: @BLOCKCHAIN_SYNCER_NEW_PEER_HEAD_001, @BLOCKCHAIN_SYNCER_VALIDATE_MESSAGE_META_001, @BLOCKCHAIN_SYNCER_STOP_001
	kit.Expensive(t)

	kit.QuietMiningLogs()

	var (
		blocktime = 2 * time.Millisecond
		nSectors  = 2
		ctx       = context.Background()
	)

	client, miner, ens := kit.EnsembleMinimal(t, kit.MockProofs(), kit.PresealSectors(nSectors))
	ens.InterconnectAll().BeginMining(blocktime)

	maddr, err := miner.ActorAddress(ctx)
	require.NoError(t, err)

	ssz, err := miner.ActorSectorSize(ctx, maddr)
	require.NoError(t, err)

	//stm: @CHAIN_STATE_MINER_POWER_001
	p, err := client.StateMinerPower(ctx, maddr, types.EmptyTSK)
	require.NoError(t, err)
	require.Equal(t, p.MinerPower, p.TotalPower)
	require.Equal(t, p.MinerPower.RawBytePower, types.NewInt(uint64(ssz)*uint64(nSectors)))

	t.Log("Seal a sector")

	miner.PledgeSectors(ctx, 1, 0, nil)

	t.Log("wait for power")

	{
		//stm: @CHAIN_STATE_MINER_CALCULATE_DEADLINE_001
		// Wait until proven.
		di, err := client.StateMinerProvingDeadline(ctx, maddr, types.EmptyTSK)
		require.NoError(t, err)

		waitUntil := di.Open + di.WPoStProvingPeriod
		t.Logf("End for head.Height > %d", waitUntil)

		ts := client.WaitTillChain(ctx, kit.HeightAtLeast(waitUntil))
		t.Logf("Now head.Height = %d", ts.Height())
	}

	nSectors++

	//stm: @CHAIN_STATE_MINER_POWER_001
	p, err = client.StateMinerPower(ctx, maddr, types.EmptyTSK)
	require.NoError(t, err)
	require.Equal(t, p.MinerPower, p.TotalPower)
	require.Equal(t, types.NewInt(uint64(ssz)*uint64(nSectors)), p.MinerPower.RawBytePower)

	t.Log("Terminate a sector")

	toTerminate := abi.SectorNumber(3)

	err = miner.SectorTerminate(ctx, toTerminate)
	require.NoError(t, err)

	msgTriggerred := false
loop:
	for {
		si, err := miner.SectorsStatus(ctx, toTerminate, false)
		require.NoError(t, err)

		t.Log("state: ", si.State, msgTriggerred)

		switch sealing.SectorState(si.State) {
		case sealing.Terminating:
			if !msgTriggerred {
				{
					p, err := miner.SectorTerminatePending(ctx)
					require.NoError(t, err)
					require.Len(t, p, 1)
					require.Equal(t, abi.SectorNumber(3), p[0].Number)
				}

				c, err := miner.SectorTerminateFlush(ctx)
				require.NoError(t, err)
				if c != nil {
					msgTriggerred = true
					t.Log("terminate message:", c)

					{
						p, err := miner.SectorTerminatePending(ctx)
						require.NoError(t, err)
						require.Len(t, p, 0)
					}
				}
			}
		case sealing.TerminateWait, sealing.TerminateFinality, sealing.Removed:
			break loop
		}

		time.Sleep(100 * time.Millisecond)
	}

	// need to wait for message to be mined and applied.
	time.Sleep(5 * time.Second)

	//stm: @CHAIN_STATE_MINER_POWER_001
	// check power decreased
	p, err = client.StateMinerPower(ctx, maddr, types.EmptyTSK)
	require.NoError(t, err)
	require.Equal(t, p.MinerPower, p.TotalPower)
	require.Equal(t, types.NewInt(uint64(ssz)*uint64(nSectors-1)), p.MinerPower.RawBytePower)

	// check in terminated set
	{
		//stm: @CHAIN_STATE_MINER_GET_PARTITIONS_001
		parts, err := client.StateMinerPartitions(ctx, maddr, 1, types.EmptyTSK)
		require.NoError(t, err)
		require.Greater(t, len(parts), 0)

		bflen := func(b bitfield.BitField) uint64 {
			l, err := b.Count()
			require.NoError(t, err)
			return l
		}

		require.Equal(t, uint64(1), bflen(parts[0].AllSectors))
		require.Equal(t, uint64(0), bflen(parts[0].LiveSectors))
	}

	//stm: @CHAIN_STATE_MINER_CALCULATE_DEADLINE_001
	di, err := client.StateMinerProvingDeadline(ctx, maddr, types.EmptyTSK)
	require.NoError(t, err)

	waitUntil := di.PeriodStart + di.WPoStProvingPeriod + 20 // slack like above
	t.Logf("End for head.Height > %d", waitUntil)
	ts := client.WaitTillChain(ctx, kit.HeightAtLeast(waitUntil))
	t.Logf("Now head.Height = %d", ts.Height())

	//stm: @CHAIN_STATE_MINER_POWER_001
	p, err = client.StateMinerPower(ctx, maddr, types.EmptyTSK)
	require.NoError(t, err)

	require.Equal(t, p.MinerPower, p.TotalPower)
	require.Equal(t, types.NewInt(uint64(ssz)*uint64(nSectors-1)), p.MinerPower.RawBytePower)
}
