package main

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	gethRPC "github.com/ethereum/go-ethereum/rpc"
	"github.com/prysmaticlabs/prysm/beacon-chain/attestation"
	"github.com/prysmaticlabs/prysm/beacon-chain/blockchain"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	"github.com/prysmaticlabs/prysm/shared/featureconfig"
	"github.com/prysmaticlabs/prysm/shared/params"
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
	params.UseDemoBeaconConfig()
	featureconfig.InitFeatureConfig(&featureconfig.FeatureFlagConfig{})

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
	attService := attestation.NewAttestationService(ctx, &attestation.Config{
		BeaconDB: db,
	})
	chainService, err := blockchain.NewChainService(ctx, &blockchain.Config{
		BeaconDB:    db,
		AttsService: attService,
		Web3Service: web3Service,
	})
	if err != nil {
		log.Fatal(err)
	}

	stateInit := make(chan time.Time)
	stateInitFeed := chainService.StateInitializedFeed()
	stateInitFeed.Subscribe(stateInit)

	attService.Start()
	defer attService.Stop()
	chainService.Start()
	defer chainService.Stop()
	web3Service.Start()
	defer web3Service.Stop()

	log.Info("Waiting for chainstart")
	<-stateInit

	// Begin the replay of the system.
	// Get the highest information.
	highestState, err := dbRO.HeadState(ctx)
	if err != nil {
		log.Fatal(err)
	}
	genesisState, err := db.HeadState(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Infof("Highest state: %d, current state: %d", highestState.Slot-params.BeaconConfig().GenesisSlot, 0)
	currentState := genesisState
	for currentSlot := currentState.Slot + 1; currentSlot <= highestState.Slot; currentSlot++ {
		log.Infof("Slot %d", currentSlot)
		newBlock, err := dbRO.BlockBySlot(ctx, currentSlot)
		if err != nil {
			log.Fatal(err)
		}
		if newBlock == nil {
			log.Warnf("no block at slot %d", currentSlot)
			continue
		}
		if err := db.SaveBlock(newBlock); err != nil {
			log.Fatal(err)
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
		_ = newHead
	}
}
