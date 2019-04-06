package main

import (
	"context"
	"github.com/prysmaticlabs/prysm/beacon-chain/blockchain"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("prefix", "state-replay")

func main() {
	db, err := db.NewDB("~/Documents/Prysmatic/Testing")
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	genesisBlock, err := db.BlockBySlot(ctx, params.BeaconConfig().GenesisSlot)
	if err != nil {
		log.Fatal(err)
	}
	genesisState, err := db.HistoricalStateFromSlot(ctx, params.BeaconConfig().GenesisSlot)
	if err != nil {
		log.Fatal(err)
	}
	highestState, err := db.HeadState(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.UpdateChainHead(ctx, genesisBlock, genesisState); err != nil {
		log.Fatal(err)
	}
	chainService, err := blockchain.NewChainService(ctx, &blockchain.Config{
		BeaconDB: db,
	})
	if err != nil {
		log.Fatal(err)
	}
	currentState := genesisState
	currentBlock := genesisBlock
	for currentState.Slot != highestState.Slot {
		newBlock, err := db.BlockBySlot(ctx, currentBlock.Slot+1)
		if err != nil {
            log.Fatal(err)
		}
		if newBlock != nil {
			newState, err := chainService.ApplyBlockStateTransition(ctx, newBlock, currentState)
			if err != nil {
				log.Error(err)
			}
			if err := chainService.ApplyForkChoiceRule(ctx, newBlock, newState); err != nil {
				log.Error(err)
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
}
