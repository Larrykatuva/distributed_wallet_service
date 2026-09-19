package cache

// Key builders keep every cache key in one place so invalidation and reads
// cannot drift apart.

func WalletKey(id string) string           { return "wallet:id:" + id }
func WalletNumberKey(number string) string { return "wallet:number:" + number }
func WalletMetaKey(id string) string       { return "wallet:meta:" + id }

func ProfileKey(id string) string               { return "profile:id:" + id }
func ProfileUsernameKey(username string) string { return "profile:username:" + username }

func TransactionKey(id string) string         { return "txn:id:" + id }
func TransactionRRNKey(rrn string) string     { return "txn:rrn:" + rrn }
func TransactionOrderKey(order string) string { return "txn:order:" + order }
func OrderUsedKey(order string) string        { return "txn:order-used:" + order }
