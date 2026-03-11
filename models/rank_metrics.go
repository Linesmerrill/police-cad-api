package models

// MetricTypeDef defines a built-in metric type that admins can use when configuring rank requirements
type MetricTypeDef struct {
	Type        string `json:"type"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// MetricTypeRegistry is the canonical list of available metric types for rank requirements.
// Each metric type maps to a specific aggregation query across the CAD data.
var MetricTypeRegistry = []MetricTypeDef{
	{"citations_issued", "Citations Issued", "Total citations issued by the officer"},
	{"warnings_issued", "Warnings Issued", "Total warnings issued by the officer"},
	{"arrests_made", "Arrests Made", "Total arrests made by the officer"},
	{"calls_created", "Calls Created", "Total calls created by the officer"},
	{"calls_responded", "Calls Responded To", "Total calls the officer was assigned to"},
	{"calls_cleared", "Calls Cleared", "Total calls cleared by the officer"},
	{"bolos_created", "BOLOs Created", "Total BOLOs created by the officer"},
	{"warrants_requested", "Warrants Requested", "Total warrants requested by the officer"},
	{"warrants_executed", "Warrants Executed", "Total warrants executed by the officer"},
}

// MetricTypeDisplayNames maps metric type keys to human-readable names
var MetricTypeDisplayNames = func() map[string]string {
	m := make(map[string]string, len(MetricTypeRegistry))
	for _, mt := range MetricTypeRegistry {
		m[mt.Type] = mt.DisplayName
	}
	return m
}()
