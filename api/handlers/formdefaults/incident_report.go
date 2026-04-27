// Package formdefaults provides built-in form templates that ship with
// every community. They are not stored in Mongo; they are merged into a
// community's template list at read time. A community can suppress a
// default by inserting a FormTemplate row with IsHidden=true and
// DefaultSlug=<slug>.
package formdefaults

import "github.com/linesmerrill/police-cad-api/models"

// IncidentReportSlug is the canonical slug for the built-in Incident
// Report template. Submissions reference this string in their
// FormTemplateSlug field.
const IncidentReportSlug = "incident-report"

// IncidentReport returns the built-in Incident Report template (sections,
// fields, and auto-fill mappings).
func IncidentReport() models.FormTemplateView {
	return models.FormTemplateView{
		ID:               "default:" + IncidentReportSlug,
		Slug:             IncidentReportSlug,
		Name:             "Incident Report",
		Description:      "Standard incident report — auto-fills from calls, citations, and arrest reports.",
		Icon:             "file-text",
		CurrentVersion:   1,
		NumberFormat:     "RR-{YYYY}-{NNNNNN}",
		VisibleToRoles:   []string{"police", "dispatch", "fire", "ems", "judicial"},
		EditableByRoles:  []string{"police", "dispatch", "fire", "ems"},
		LinkableEntities: []string{"civilian", "vehicle", "firearm", "call", "citation", "arrestReport"},
		IsDefault:        true,
		Sections:         incidentReportSections(),
	}
}

func incidentReportSections() []models.FormSection {
	return []models.FormSection{
		{
			ID: "incidentInfo", Title: "Incident Info",
			Fields: []models.FormField{
				{ID: "incidentNumber", Type: "text", Label: "Incident #", Placeholder: "RR-YYYY-NNNNNN — leave blank to auto-generate"},
				{ID: "callType", Type: "text", Label: "Call Type", Required: true,
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "title"},
					}},
				{ID: "priorityLevel", Type: "select", Label: "Priority Level", Options: []string{"1 - Critical", "2 - High", "3 - Routine", "4 - Low"},
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "classifier[0]"},
					}},
				{ID: "status", Type: "select", Label: "Status", Options: []string{"Open", "Active", "Cleared", "Closed"}},
				{ID: "date", Type: "date", Label: "Date", DefaultExpr: "today",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "createdAt"},
						{Source: "arrestReport", Path: "incidentDate"},
					}},
				{ID: "timeDispatched", Type: "time", Label: "Time Dispatched",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "createdAt"},
					}},
				{ID: "timeArrived", Type: "time", Label: "Time Arrived"},
				{ID: "timeCleared", Type: "time", Label: "Time Cleared",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "updatedAt"},
					}},
			},
		},
		{
			ID: "locationDetails", Title: "Location Details",
			Fields: []models.FormField{
				{ID: "address", Type: "text", Label: "Address",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "details"},
						{Source: "arrestReport", Path: "incidentLocation"},
					}},
				{ID: "city", Type: "text", Label: "City"},
				{ID: "zipCode", Type: "text", Label: "Zip Code"},
				{ID: "premisesType", Type: "select", Label: "Premises Type", Options: []string{"Residence", "Business", "Public Way", "School", "Park", "Vehicle", "Other"}},
			},
		},
		{
			ID: "unitInformation", Title: "Unit Information",
			Fields: []models.FormField{
				{ID: "primaryUnit", Type: "text", Label: "Primary Unit", DefaultExpr: "auth.username"},
				{ID: "assistingUnits", Type: "multiSelect", Label: "Assisting Units",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "assignedTo"},
					}},
				{ID: "supervisorNotified", Type: "checkbox", Label: "Supervisor Notified"},
				{ID: "reportingOfficer", Type: "text", Label: "Reporting Officer", Required: true, DefaultExpr: "auth.username",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "arrestReport", Path: "officer.name"},
					}},
				{ID: "badgeNumber", Type: "text", Label: "Badge #", DefaultExpr: "auth.badgeNumber",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "arrestReport", Path: "officer.badgeNumber"},
					}},
			},
		},
		{
			ID: "reportingParty", Title: "Reporting Party",
			Fields: []models.FormField{
				{ID: "name", Type: "text", Label: "Name"},
				{ID: "dob", Type: "date", Label: "DOB"},
				{ID: "phone", Type: "text", Label: "Phone"},
				{ID: "address", Type: "text", Label: "Address"},
			},
		},
		{
			ID: "suspects", Title: "Suspects", Repeatable: true,
			Fields: []models.FormField{
				{ID: "name", Type: "text", Label: "Name",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "arrestReport", Path: "arrestee.name"},
						{Source: "citation", Path: "civilian.firstName civilian.lastName"},
					}},
				{ID: "dob", Type: "date", Label: "DOB",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "arrestReport", Path: "arrestee.dob"},
						{Source: "citation", Path: "civilian.birthday"},
					}},
				{ID: "description", Type: "textarea", Label: "Description"},
				{ID: "status", Type: "select", Label: "Status", Options: []string{"At Large", "Detained", "Arrested", "Cited", "Released"}},
			},
		},
		{
			ID: "victims", Title: "Victims", Repeatable: true,
			Fields: []models.FormField{
				{ID: "name", Type: "text", Label: "Name"},
				{ID: "dob", Type: "date", Label: "DOB"},
				{ID: "injuryStatus", Type: "select", Label: "Injury Status", Options: []string{"None", "Minor", "Serious", "Critical", "Fatal"}},
			},
		},
		{
			ID: "narrative", Title: "Narrative",
			Fields: []models.FormField{
				{ID: "narrative", Type: "textarea", Label: "Narrative", Required: true,
					Placeholder: "On [DATE] at approximately [TIME], Officers with the [DEPARTMENT] were dispatched to [LOCATION] regarding a report of [CALL TYPE].\n\nUpon arrival, Officers contacted [NAME] who stated…\n\nInvestigation revealed…",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "call", Path: "details"},
						{Source: "arrestReport", Path: "narrative"},
					}},
			},
		},
		{
			ID: "charges", Title: "Charges / Violations", Repeatable: true,
			Fields: []models.FormField{
				{ID: "charge", Type: "penalCodePicker", Label: "Charge",
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "citation", Path: "fines[].fineType"},
					}},
				{ID: "rcwCode", Type: "text", Label: "RCW Code", Placeholder: "e.g. RCW 9A.56.050"},
				{ID: "disposition", Type: "select", Label: "Disposition", Options: []string{"Arrest", "Citation", "Warning"},
					PopulateFrom: []models.FormFieldPopulate{
						{Source: "citation", Path: "type"},
						{Source: "arrestReport", Path: "@const:Arrest"},
					}},
			},
		},
		{
			ID: "evidence", Title: "Evidence / Property", Repeatable: true,
			Fields: []models.FormField{
				{ID: "itemNumber", Type: "text", Label: "Item #"},
				{ID: "description", Type: "textarea", Label: "Description"},
				{ID: "collectedBy", Type: "userPicker", Label: "Collected By"},
				{ID: "loggedIntoEvidence", Type: "checkbox", Label: "Logged Into Evidence"},
			},
		},
		{
			ID: "medicalFire", Title: "Medical / Fire Response",
			Fields: []models.FormField{
				{ID: "emsRequested", Type: "checkbox", Label: "EMS Requested"},
				{ID: "fireDepartment", Type: "text", Label: "Fire Department", Placeholder: "e.g. Clark County Fire District 6"},
				{ID: "transportedTo", Type: "text", Label: "Transported To", Placeholder: "Hospital name"},
			},
		},
		{
			ID: "dispatch", Title: "Dispatch Notes",
			Fields: []models.FormField{
				{ID: "source", Type: "select", Label: "Source", Options: []string{"911", "Non-Emergency", "Officer Initiated", "Walk-In"}},
				{ID: "additionalRemarks", Type: "textarea", Label: "Additional Remarks"},
			},
		},
		{
			ID: "approval", Title: "Approval",
			Fields: []models.FormField{
				{ID: "reportingOfficerSignature", Type: "text", Label: "Reporting Officer Signature", DefaultExpr: "auth.username", HelpText: "Auto-signed when submitted."},
				{ID: "supervisorApproval", Type: "text", Label: "Supervisor Approval", HelpText: "Optional."},
				{ID: "dateSubmitted", Type: "date", Label: "Date Submitted", DefaultExpr: "today"},
			},
		},
	}
}

// All returns every built-in default template, keyed by slug. Add new
// defaults here.
func All() map[string]models.FormTemplateView {
	return map[string]models.FormTemplateView{
		IncidentReportSlug: IncidentReport(),
	}
}
