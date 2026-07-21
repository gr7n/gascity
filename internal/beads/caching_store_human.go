package beads

// HumanResponderHandle exposes the backing store's narrow human-decision
// capability through the cache wrapper.
func (c *CachingStore) HumanResponderHandle() (HumanResponder, bool) {
	return HumanResponderFor(c.backing)
}

// RespondToHuman forwards the atomic backend action and evicts the cached row.
// The next read must observe the response/close written by the backend rather
// than a locally fabricated projection.
func (c *CachingStore) RespondToHuman(id, response, actor string) error {
	responder, ok := HumanResponderFor(c.backing)
	if !ok {
		return ErrHumanResponseUnsupported
	}
	if err := responder.RespondToHuman(id, response, actor); err != nil {
		c.evictForConditionalWrite(id)
		return err
	}
	fresh, err := c.backing.Get(id)
	c.evictForConditionalWrite(id)
	if err == nil && fresh.Status == "closed" {
		c.notifyChange("bead.closed", fresh)
	}
	return nil
}
