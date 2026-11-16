package handlers

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/linesmerrill/police-cad-api/config"
)

// The narrative is the one long free-text field on a report an officer writes
// by hand, and the CAD's own forms cap it: the arrest form in
// police-cad/views/arrest-modal.ejs and police-cad/public/js/cd-action-forms.js,
// the Reports editor in police-cad/views/report-edit.ejs, and the mobile
// arrest form. Those caps are client-side only, so a direct call to this API,
// or a mobile build released before the cap existed, could still store more.
// Enforcing it here makes 1000 the real limit rather than a suggestion.
//
// Deliberately separate from maxReportNarrativeLen in developer_api_reports.go.
// That is the Developer API's own draft limit (8000) for an integration's
// machine-written prose, which carries provenance labels and is reviewed by a
// human before anything is filed. This cap is for what a person types into a
// form.
const (
	// maxHumanNarrativeLen is the cap, in characters.
	maxHumanNarrativeLen = 1000

	// narrativeTooLongMessage is what the caller is told. The clients show
	// this string verbatim, so it reads as a sentence.
	narrativeTooLongMessage = "Narrative can't be longer than 1000 characters."

	// narrativeFieldID is the form field whose value this cap applies to.
	// Form submission data is keyed by field id, and the built-in Incident
	// Report's narrative section uses "narrative" for both
	// (api/handlers/formdefaults/incident_report.go).
	narrativeFieldID = "narrative"
)

// narrativeOverLimit reports whether v is a narrative longer than the cap.
//
// Characters, not bytes: a narrative written in a language that does not fit
// in one byte per character, or one carrying an emoji, gets the same 1000
// characters as plain ASCII. Counting len() would have cut such a report to a
// third of the length.
//
// A value that is not a string is not a narrative. Nothing here is the place
// to reject the wrong type for a field - the decode and the form's own
// handling own that - so a non-string is left alone.
func narrativeOverLimit(v interface{}) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return utf8.RuneCountInString(s) > maxHumanNarrativeLen
}

// rejectLongNarrative writes a 400 and reports true when v is over the cap,
// so a handler can `if rejectLongNarrative(w, v) { return }`. The response is
// the same envelope every other validation failure in these handlers returns.
func rejectLongNarrative(w http.ResponseWriter, v interface{}) bool {
	if !narrativeOverLimit(v) {
		return false
	}
	config.ErrorStatus(narrativeTooLongMessage, http.StatusBadRequest, w,
		fmt.Errorf("narrative is longer than %d characters", maxHumanNarrativeLen))
	return true
}

// rejectLongNarrativeField does the same for a form submission's data map,
// which is an untyped bag keyed by field id. A body that does not carry the
// narrative field at all leaves whatever is stored untouched, so there is
// nothing to check.
func rejectLongNarrativeField(w http.ResponseWriter, data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	v, ok := data[narrativeFieldID]
	if !ok {
		return false
	}
	return rejectLongNarrative(w, v)
}
