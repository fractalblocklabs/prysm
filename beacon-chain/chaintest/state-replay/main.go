package main

import (
	"context"
	"time"

	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	gethRPC "github.com/ethereum/go-ethereum/rpc"
	"github.com/prysmaticlabs/prysm/beacon-chain/blockchain"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/blocks"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("prefix", "state-replay")

func main() {
	dbRO, err := db.NewDB("/tmp")
	if err != nil {
		log.Fatal(err)
	}
	db.ClearDB("/tmp/data")
	db, err := db.NewDB("/tmp/data")
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// Chain head
	head, err := dbRO.ChainHead()
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("Read-only db has a chainhead slot of %d", head.Slot)

	// Setup a chain service and powchain service.
	rpcClient, err := gethRPC.Dial("wss://goerli.prylabs.net/websocket")
	if err != nil {
		log.Fatalf("Access to PoW chain is required for validator. Unable to connect to Geth node: %v", err)
	}
	powClient := ethclient.NewClient(rpcClient)
	cfg := &powchain.Web3ServiceConfig{
		Endpoint:        "wss://goerli.prylabs.net/websocket",
		DepositContract: common.HexToAddress("0x76F8c0868EA2a52C9515d4D042243D1f11b3a29D"),
		Client:          powClient,
		Reader:          powClient,
		Logger:          powClient,
		BlockFetcher:    powClient,
		ContractBackend: powClient,
		BeaconDB:        db,
	}
	web3Service, err := powchain.NewWeb3Service(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	chainService, err := blockchain.NewChainService(ctx, &blockchain.Config{
		BeaconDB:    db,
		Web3Service: web3Service,
	})
	if err != nil {
		log.Fatal(err)
	}
	// Process past logs.
	//web3Service.InitializeValues()

	stateInit := make(chan time.Time)
	stateInitFeed := chainService.StateInitializedFeed()
	stateInitFeed.Subscribe(stateInit)

	chainService.Start()
	defer chainService.Stop()
	web3Service.Start()
	defer web3Service.Stop()

	log.Info("Waiting for chainstart")
	<-stateInit

	// Begin the replay of the system.
	chainStartDeposits := make([]*pb.Deposit, 32)
	deposits := db.AllDeposits(ctx, nil)
	for i := 0; i < 32; i++ {
		chainStartDeposits[i] = deposits[i]
	}

	genesisState, err := state.GenesisBeaconState(chainStartDeposits, 0, &pb.Eth1Data{
		BlockHash32:       []byte{},
		DepositRootHash32: []byte{},
	})
	if err != nil {
		log.Fatal(err)
	}
	stateRoot, err := hashutil.HashProto(genesisState)
	if err != nil {
		log.Fatal(err)
	}
	genesisBlock := blocks.NewGenesisBlock(stateRoot[:])
	if err := db.SaveBlock(genesisBlock); err != nil {
		log.Fatal(err)
	}

	// Get the highest information.
	highestState, err := dbRO.HeadState(ctx)
	if err != nil {
		log.Fatal(err)
	}
	//	if err := db.UpdateChainHead(ctx, genesisBlock, genesisState); err != nil {
	//		log.Fatal(err)
	//	}
	log.Infof("Highest state: %d, current state: %d", highestState.Slot-params.BeaconConfig().GenesisSlot, 0)
	currentState := genesisState
	currentBlock := genesisBlock
	for currentState.Slot != highestState.Slot {
		newBlock, err := dbRO.BlockBySlot(ctx, currentBlock.Slot+1)
		if err != nil {
			log.Fatal(err)
		}
		if newBlock == nil {
			log.Warnf("no block at slot %d", currentState.Slot+1)
			continue
		}

		newState, err := chainService.ApplyBlockStateTransition(ctx, newBlock, currentState)
		if err != nil {
			log.Fatal(err)
		}
		if err := chainService.ApplyForkChoiceRule(ctx, newBlock, newState); err != nil {
			log.Fatal(err)
		}
		newState, err = db.HeadState(ctx)
		if err != nil {
			log.Fatal(err)
		}
		newHead, err := db.ChainHead()
		if err != nil {
			log.Fatal(err)
		}
		currentState = newState
		currentBlock = newHead
	}
}
