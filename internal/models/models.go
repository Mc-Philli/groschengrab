package models

type User struct {
	ID           int64
	Name         string
	PasswordHash string
	Role         string
}

type Account struct {
	ID      int64
	Name    string
	Type    string
	Balance float64
}

type Transaction struct {
	ID          int64
	AccountID   int64
	UserID      int64
	Amount      float64
	BookedAt    string
	Category    string
	Description string
	Kind        string // "income" oder "expense"
}

type Receipt struct {
	ID            int64
	TransactionID *int64
	ImagePath     string
	OCRRawText    string
	Status        string // "open" oder "checked"
}

type Holding struct {
	ID            int64
	Ticker        string
	Quantity      float64
	PurchasePrice float64
	PurchaseDate  string
}

type OtherAsset struct {
	ID           int64
	Name         string
	Type         string
	CurrentValue float64
}

// TransactionView ist eine für die Anzeige aufbereitete Buchung
// (Konto- und Nutzername statt nur IDs).
type TransactionView struct {
	ID          int64
	AccountName string
	UserName    string
	Amount      float64
	BookedAt    string
	Category    string
	Description string
	Kind        string
}
