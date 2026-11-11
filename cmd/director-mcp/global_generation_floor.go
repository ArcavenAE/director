package main

import "context"

// generationFloor is the highest ack floor among the departed durables of this
// seat's earlier generations on the hub stream, and the name of the durable it
// came from. Stub: no floor.
func (g *globalTier) generationFloor(ctx context.Context, agentID, self string) (uint64, string) {
	return 0, ""
}
