package models

// ItemType represents the standardized types of items in the system
type ItemType string

// Predefined ItemType values
const (
	ItemTypeUserReport  ItemType = "USER_REPORT"
	ItemTypeAdReport    ItemType = "AD_REPORT"
	ItemTypeContentFlag ItemType = "CONTENT_FLAG"
)

// ValidItemTypes returns all valid ItemType values
func ValidItemTypes() []ItemType {
	return []ItemType{
		ItemTypeUserReport,
		ItemTypeAdReport,
		ItemTypeContentFlag,
	}
}

// IsValid checks if the ItemType value is one of the predefined constants
func (t ItemType) IsValid() bool {
	for _, validType := range ValidItemTypes() {
		if t == validType {
			return true
		}
	}
	return false
}

// String returns the string representation of the ItemType
func (t ItemType) String() string {
	return string(t)
}
