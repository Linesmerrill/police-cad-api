package models

// ReportType represents the standardized types of items in the system
type ReportType string

// Predefined ReportType values
const (
	ReportTypeUserReport  ReportType = "USER_REPORT"
	ReportTypeAdReport    ReportType = "AD_REPORT"
	ReportTypeContentFlag ReportType = "CONTENT_FLAG"
	// ReportTypeCommunityReport is a report about a community itself, filed
	// from its page. AD_REPORT stays the type for a report about a promoted
	// community card in the app.
	ReportTypeCommunityReport ReportType = "COMMUNITY_REPORT"
)

// ValidReportTypes returns all valid ReportType values
func ValidReportTypes() []ReportType {
	return []ReportType{
		ReportTypeUserReport,
		ReportTypeAdReport,
		ReportTypeContentFlag,
		ReportTypeCommunityReport,
	}
}

// IsValid checks if the ReportType value is one of the predefined constants
func (t ReportType) IsValid() bool {
	for _, validType := range ValidReportTypes() {
		if t == validType {
			return true
		}
	}
	return false
}

// String returns the string representation of the ReportType
func (t ReportType) String() string {
	return string(t)
}
