package models

import "strings"

// MetricTypeDef defines a built-in metric type that admins can use when configuring rank requirements
type MetricTypeDef struct {
	Type            string   `json:"type"`
	DisplayName     string   `json:"displayName"`
	Description     string   `json:"description"`
	DepartmentTypes []string `json:"departmentTypes"`
}

// MetricTypeRegistry is the canonical list of available metric types for rank requirements.
// Each metric type maps to a specific aggregation query across the CAD data.
// DepartmentTypes controls which department types see this metric in their dropdown.
var MetricTypeRegistry = []MetricTypeDef{
	// Police metrics
	{"citations_issued", "Citations Issued", "Total citations issued by the officer", []string{"police"}},
	{"warnings_issued", "Warnings Issued", "Total warnings issued by the officer", []string{"police"}},
	{"arrests_made", "Arrests Made", "Total arrests made by the officer", []string{"police"}},

	// Shared: police, ems, fire, dispatch
	{"calls_created", "Calls Created", "Total calls created", []string{"police", "dispatch"}},
	{"calls_responded", "Calls Responded To", "Total calls assigned to this member", []string{"police", "ems", "fire"}},
	{"calls_cleared", "Calls Cleared", "Total calls cleared by this member", []string{"police", "ems", "fire"}},
	{"bolos_created", "BOLOs Created", "Total BOLOs created", []string{"police", "ems", "fire", "dispatch"}},

	// Police warrants
	{"warrants_requested", "Warrants Requested", "Total warrants requested by the officer", []string{"police"}},
	{"warrants_executed", "Warrants Executed", "Total warrants executed by the officer", []string{"police"}},

	// EMS / Fire
	{"medical_reports_created", "Medical Reports Created", "Total medical reports filed", []string{"ems", "fire"}},

	// Dispatch
	{"calls_dispatched", "Calls Dispatched", "Total calls dispatched", []string{"dispatch"}},

	// Judicial
	{"warrants_reviewed", "Warrants Reviewed", "Total warrants reviewed as judge", []string{"judicial"}},
	{"court_cases_completed", "Court Cases Completed", "Total court cases completed as judge", []string{"judicial"}},
}

// MetricTypeDisplayNames maps metric type keys to human-readable names
var MetricTypeDisplayNames = func() map[string]string {
	m := make(map[string]string, len(MetricTypeRegistry))
	for _, mt := range MetricTypeRegistry {
		m[mt.Type] = mt.DisplayName
	}
	return m
}()

// MetricTypesForDepartment returns only the metrics applicable to the given department type.
// The deptType should be lowercase: "police", "ems", "fire", "dispatch", "civilian", "judicial".
func MetricTypesForDepartment(deptType string) []MetricTypeDef {
	deptType = strings.ToLower(deptType)
	var result []MetricTypeDef
	for _, mt := range MetricTypeRegistry {
		for _, dt := range mt.DepartmentTypes {
			if dt == deptType {
				result = append(result, mt)
				break
			}
		}
	}
	return result
}
