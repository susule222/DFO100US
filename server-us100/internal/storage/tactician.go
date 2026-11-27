package storage

// TacticianEnd reads the server-owned account entitlement, not a client flag.
func (s *Store) TacticianEnd(account int64) (int64, error) {
	var ends map[byte]int64
	_, err := s.db.Get("us224_account_premium", idKey(account), &ends)
	return ends[27], err
}
