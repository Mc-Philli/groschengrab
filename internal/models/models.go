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
	ToAccountID *int64 // nur bei Transfers gesetzt
	UserID      int64
	Amount      float64
	BookedAt    string
	Category    string
	Description string
	Kind        string // "income", "expense" oder "transfer"
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
	PurchasePrice float64 // pro Stück, in der Notierungswährung des Tickers
	PurchaseDate  string
	Depot         string // z. B. "Saxo", "WillBe"; leer = keinem Depot zugeordnet
	Name          string // optionaler Anzeigename, z. B. "Vanguard FTSE All-World"
	Source        string // "manual", "savings_plan" oder "correction"
	SavingsPlanID *int64
}

// PositionView fasst alle Tranchen (Käufe) eines Tickers in einem Depot zu
// einer Position zusammen, mit live abgefragtem Kurs.
type PositionView struct {
	Depot          string
	Ticker         string
	Name           string
	Quantity       float64
	CostBasis      float64 // Summe Anzahl × Kaufpreis (Notierungswährung)
	AvgPrice       float64
	Lots           []Holding
	PriceAvailable bool
	CurrentPrice   float64
	Currency       string
	CurrentValue   float64 // Notierungswährung
	GainAbsolute   float64 // Notierungswährung
	GainPercent    float64
	FXAvailable    bool
	ValueCHF       float64
	GainCHF        float64
}

// DepotView ist ein Depot (z. B. Saxo) mit seinen Positionen.
type DepotView struct {
	Name      string
	Positions []PositionView
	ValueCHF  float64
	GainCHF   float64
	Missing   int // Positionen ohne Kurs oder Wechselkurs (nicht im Wert enthalten)
}

// SavingsPlan ist ein Sparplan: fester CHF-Betrag pro Monat, aufgeteilt auf
// mehrere ETFs.
type SavingsPlan struct {
	ID             int64
	Depot          string
	Amount         float64
	DayOfMonth     int
	StartDate      string
	AccountID      *int64
	AccountName    string
	UserID         int64
	Active         bool
	LastExecutedOn string
	LastError      string
	NextDue        string
	Items          []SavingsPlanItem
}

type SavingsPlanItem struct {
	Ticker string
	Name   string
	Weight float64 // Prozent
}

type OtherAsset struct {
	ID           int64
	Name         string
	Type         string
	CurrentValue float64
	UpdatedAt    string // Stichtag der jüngsten Bewertung (YYYY-MM-DD)
	IsDebt       bool   // Schulden werden vom Vermögen abgezogen
}

type Category struct {
	ID   int64
	Name string
}

// CategoryRule ordnet Buchungen anhand eines Stichworts automatisch einer
// Kategorie zu (z. B. "Lidl" -> "Lebensmittel").
type CategoryRule struct {
	ID       int64
	Keyword  string
	Category string
}

// TransactionView ist eine für die Anzeige aufbereitete Buchung
// (Konto- und Nutzername statt nur IDs).
type TransactionView struct {
	ID            int64
	AccountName   string
	ToAccountName string // nur bei Transfers gefüllt
	UserName      string
	Amount        float64
	BookedAt      string
	Category      string
	Description   string
	Kind          string
}
