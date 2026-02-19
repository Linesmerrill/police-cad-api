package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// GetCommunityPenalCodesHandler returns community penal codes, initializing defaults if empty
func (c Community) GetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to find community", http.StatusNotFound, w, err)
		return
	}

	penalCodes := community.Details.PenalCodes
	// Lazy initialization: if no categories exist, set defaults
	if len(penalCodes.Categories) == 0 {
		penalCodes = defaultCommunityPenalCodes()
		// Persist defaults so this only happens once
		filter := bson.M{"_id": cID}
		update := bson.M{"$set": bson.M{"community.penalCodes": penalCodes}}
		_ = c.DB.UpdateOne(ctx, filter, update)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(penalCodes)
}

// SetCommunityPenalCodesHandler updates the community penal codes
func (c Community) SetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	var penalCodesData models.CommunityPenalCode
	if err := json.NewDecoder(r.Body).Decode(&penalCodesData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.penalCodes": penalCodesData,
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update community penal codes", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community penal codes updated successfully"}`))
}

// ResetCommunityPenalCodesHandler resets community penal codes to defaults
func (c Community) ResetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	defaults := defaultCommunityPenalCodes()

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.penalCodes": defaults,
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to reset community penal codes", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(defaults)
}

// defaultCommunityPenalCodes returns the default set of penal codes for a community.
// These match the static data in police-cad/app/penal-code/data.ts.
func defaultCommunityPenalCodes() models.CommunityPenalCode {
	return models.CommunityPenalCode{
		Currency: "USD",
		Categories: []models.PenalCodeCategory{
			{
				ID:       "traffic-violations",
				Name:     "Traffic Violations",
				Subtitle: "May result in suspended or revoked license, a fine and possible jail time",
				Icon:     "fa-car",
				Color:    "#3b82f6",
				Columns:  []string{"name", "jailTime", "fine", "explanation"},
				Violations: []models.PenalCodeViolation{
					{Name: "Reckless Driving", JailTime: "30 seconds", Fine: 1000, Explanation: "Careless and unlawful operation of a vehicle."},
					{Name: "Driving without a license", JailTime: "20 seconds", Fine: 750, Explanation: "Operating a motor vehicle without proper paperwork."},
					{Name: "Driving without headlights or tail lights", JailTime: "N/A", Fine: 150, Explanation: "Operation of a vehicle without use of headlights or tail lights in an appropriate situation."},
					{Name: "Excessive vehicle noise", JailTime: "N/A", Fine: 200, Explanation: "Unnecessary or too much sound."},
					{Name: "Failure to obey a traffic control device", JailTime: "N/A", Fine: 250, Explanation: "Violation of intersection lights, stop and give way signs."},
					{Name: "Failure to maintain lanes", JailTime: "N/A", Fine: 500, Explanation: "Being in multiple lanes at once or swerving in between lanes."},
					{Name: "Failure to yield to a pedestrian", JailTime: "N/A", Fine: 250, Explanation: "Failing to yield to a pedestrian in a marked crosswalk."},
					{Name: "Failure to yield to an emergency vehicle", JailTime: "N/A", Fine: 500, Explanation: "Failing to yield to an Emergency vehicle with red and or blue lights."},
					{Name: "Illegal U-Turn", JailTime: "N/A", Fine: 200, Explanation: "U-Turn conducted in either a disallowed zone or an unsafe turn."},
					{Name: "Illegal parking", JailTime: "N/A", Fine: 250, Explanation: "Parking illegally in a zone disallowing parking."},
					{Name: "Impeding flow of traffic", JailTime: "N/A", Fine: 400, Explanation: "Interrupting the flow of traffic in anyway for an extended period of time."},
					{Name: "Driving without insurance", JailTime: "N/A", Fine: 450, Explanation: "Self explanatory"},
					{Name: "Speeding 10-19", JailTime: "N/A", Fine: 150, Explanation: "Failing to obey posted speed signs."},
					{Name: "Speeding 20-29", JailTime: "N/A", Fine: 250, Explanation: "Failing to obey posted speed signs."},
					{Name: "Speeding 30 or over", JailTime: "30 seconds", Fine: 500, Explanation: "Failing to obey posted speed signs."},
					{Name: "Unlawful vehicle modification", JailTime: "N/A", Fine: 500, Explanation: "Red and blue underglow, pure black window tint or police horn."},
					{Name: "Unroadworthy vehicle", JailTime: "N/A", Fine: 350, Explanation: "Operation of a motorized vehicle that is not fit to be operated on a public roads. No license plates, expired tabs, cracked windshield."},
					{Name: "Possession of burglary Tools", JailTime: "35 seconds", Fine: 800, Explanation: "A person who has in their possession tools necessary to commit burglary such as a crowbar with a screwdriver, shimmy, or other appropriate items."},
					{Name: "Improper use of a motor vehicle", JailTime: "N/A", Fine: 400, Explanation: "Improper use of a motor vehicle like not wearing a helmet on a motorcycle, or seatbelt in car."},
				},
			},
			{
				ID:       "petty-misdemeanors",
				Name:     "Petty Misdemeanors",
				Subtitle: "A small violation, prohibited by a statute. But it's not a crime. Usually results in a fine",
				Icon:     "fa-scale-balanced",
				Color:    "#f59e0b",
				Columns:  []string{"name", "jailTime", "fine", "explanation"},
				Violations: []models.PenalCodeViolation{
					{Name: "Disorderly conduct", JailTime: "15 seconds", Fine: 250, Explanation: "Unruly behaviour constituting a minor offense."},
					{Name: "Disturbing the peace", JailTime: "20 seconds", Fine: 300, Explanation: "When a person's words or conduct jeopardizes another's rights to peace and tranquility."},
					{Name: "Loitering", JailTime: "N/A", Fine: 250, Explanation: "To delay or linger in the vicinity of a posted property with no purpose or lawful reason."},
					{Name: "Public intoxication", JailTime: "25 seconds", Fine: 800, Explanation: "A person who is found in any public place under the influence of alcohol."},
					{Name: "Jaywalking", JailTime: "N/A", Fine: 100, Explanation: "Crossing a public road at an otherwise unmarked crossing."},
				},
			},
			{
				ID:       "misdemeanors",
				Name:     "Misdemeanors",
				Subtitle: "Results in a fine, jail sentence or both depending on the crime",
				Icon:     "fa-gavel",
				Color:    "#f97316",
				Columns:  []string{"name", "jailTime", "fine", "explanation"},
				Violations: []models.PenalCodeViolation{
					{Name: "Assault", JailTime: "1 Minute", Fine: 3500, Explanation: "A person who intentionally put another in the reasonable belief of physical harm or offensive contact."},
					{Name: "Aggravated Assault", JailTime: "1 minute 20 seconds", Fine: 6000, Explanation: "A person who continuously intentionally put another in the reasonable belief of severe physical harm or offensive contact."},
					{Name: "Aiding and abetting", JailTime: "45 seconds", Fine: 2000, Explanation: "The act of aiding somebody or a group in the process of a crime."},
					{Name: "Battery", JailTime: "1 minute", Fine: 2500, Explanation: "A person who uses a unlawful force or violence to cause physical harm."},
					{Name: "Aggravated Battery", JailTime: "1 minute 15 seconds", Fine: 3500, Explanation: "A person who uses great or continued force or violence to cause physical harm."},
					{Name: "Breaking and entering", JailTime: "45 seconds", Fine: 1800, Explanation: "The action of forcing your way into a property you do not have permission to be in."},
					{Name: "Brandishing a firearm", JailTime: "30 seconds", Fine: 800, Explanation: "The act of holding or pointing a unconcealed weapon on or attached to your body."},
					{Name: "Bribery", JailTime: "45 seconds", Fine: 2000, Explanation: "Attempting to give items of value and worth to receive a favorable outcome."},
					{Name: "Contempt of court", JailTime: "30 seconds", Fine: 2500, Explanation: "A person who willfully disobeys, disrespects, or otherwise infringes the verbal or written authority of the court."},
					{Name: "Destruction of property", JailTime: "30 seconds", Fine: 1500, Explanation: "The act of inflicting damage to property public or private."},
					{Name: "Drug possession Class: A, B, C", JailTime: "A) 1 min 30 sec, B) 1 min, C) 50 sec", Fine: 2000, Explanation: "Possession of one or less pound."},
					{Name: "DUI/DWI", JailTime: "1 minute", Fine: 3000, Explanation: "Operating a motorized vehicle while under the influence of alcohol or drugs."},
					{Name: "Failure to follow a lawful command", JailTime: "1 minute", Fine: 1000, Explanation: "Willingly disobeying a lawful command given by a government official."},
					{Name: "Failure to identify to a LEO", JailTime: "1 minute", Fine: 1200, Explanation: "A person who is detained or under arrest by a LEO and fails to provide their name or ID."},
					{Name: "Failure to pay fines", JailTime: "2 minutes 30 seconds", Explanation: "A person who fails to pay a fine or court ordered fee."},
					{Name: "Failure to have a valid permit", JailTime: "1 minute", Fine: 1500, Explanation: "Not having a valid permit."},
					{Name: "Filing a false complaint", JailTime: "1 minute 30 seconds", Fine: 1500, Explanation: "A person who knowingly files a false complaint, statement or document."},
					{Name: "Indecent exposure", JailTime: "2 minutes", Fine: 1500, Explanation: "A person who intentionally exposes their naked body or genitalia."},
					{Name: "Illegal street racing", JailTime: "1 minute 30 seconds", Fine: 4000, Explanation: "Unsanctioned and illegal form of auto racing that occurs on a public road."},
					{Name: "Accessory to street racing", JailTime: "1 minute", Fine: 2000, Explanation: "Helping and or planning a street racing event."},
					{Name: "Obstruction of justice", JailTime: "2 minutes 30 seconds", Fine: 1250, Explanation: "An action that shows a clear motivated attempt to stop/halt a law enforcement officer from doing their job."},
					{Name: "Providing false information to a LEO", JailTime: "2 minutes", Fine: 800, Explanation: "Providing a government official with false information."},
					{Name: "Public endangerment", JailTime: "1 minute", Fine: 1600, Explanation: "The act of placing the public in any kind of way."},
					{Name: "Stalking", JailTime: "2 minutes", Fine: 3000, Explanation: "A person that follows, threatens or harasses another individual."},
					{Name: "Sexual assault", JailTime: "3 minutes", Fine: 3000, Explanation: "A person who commits verbal abuse for the purpose of sexual arousal."},
					{Name: "Trespassing", JailTime: "2 minutes", Fine: 500, Explanation: "Entering private property that you do not have permission to reside in."},
					{Name: "Theft", JailTime: "2 minutes 30 seconds", Fine: 1000, Explanation: "A person who takes or steals another's property."},
					{Name: "Unlawful discharge of a firearm", JailTime: "1 minute 30 seconds", Fine: 800, Explanation: "Discharge of a firearm without due cause or justifiable motive."},
					{Name: "Vandalism", JailTime: "1 minute 30", Fine: 1500, Explanation: "A person that defaces, damages, or destroys property which belongs to another."},
					{Name: "Withholding info from a LEO", JailTime: "2 minutes", Explanation: "A person who intentionally withholds information from a LEO."},
					{Name: "Failure to stop for a police vehicle", JailTime: "2 minutes", Fine: 750, Explanation: "Self explanatory"},
					{Name: "Wasting police time", JailTime: "3 minutes", Explanation: "A person who intentionally wastes a LEO's time."},
				},
			},
			{
				ID:       "felonies",
				Name:     "Felonies",
				Subtitle: "A crime, typically one involving violence, regarded as more serious than a misdemeanor, and punishable by imprisonment",
				Icon:     "fa-handcuffs",
				Color:    "#ef4444",
				Columns:  []string{"name", "jailTime", "explanation"},
				Violations: []models.PenalCodeViolation{
					{Name: "Armed Robbery", JailTime: "3 minutes 30 seconds", Explanation: "A person who takes property from the possession of another with a weapon."},
					{Name: "Accessory to armed robbery", JailTime: "2 minutes", Explanation: "A person who helps commit or plan to take property from the possession of another with a weapon."},
					{Name: "Arson", JailTime: "2 minutes 30 seconds", Explanation: "A person who intentionally malicious sets fire to any structure, forest land, or property."},
					{Name: "Assault with a deadly weapon", JailTime: "3 minutes 45 seconds", Explanation: "A person who attempts to cause or threaten immediate harm to another using a weapon to communicate that threat."},
					{Name: "Accessory to assault with a deadly weapon", JailTime: "3 minutes", Explanation: "A person who helps commit or plan to attempt to cause or threaten immediate harm to another using a weapon to communicate that threat."},
					{Name: "Assault on a LEO", JailTime: "4 minutes", Explanation: "A person who intentionally put another in the reasonable belief or physical harm or offensive contact to a LEO."},
					{Name: "Attempted murder", JailTime: "4 minutes", Explanation: "A person who deliberately attempt to kill or cause life threatening."},
					{Name: "Accessory to Attempted murder", JailTime: "3 minutes 30 seconds", Explanation: "A person who deliberately helps or plans to attempt to kill or cause life threatening harm."},
					{Name: "Attempted murder of a LEO", JailTime: "4 minutes 45 seconds", Explanation: "A person who deliberately attempt to kill or cause life threatening harm."},
					{Name: "Accessory to attempted murder of a LEO", JailTime: "4 minutes", Explanation: "A person who deliberately helps or cause life threatening harm of a LEO."},
					{Name: "Corruption of a government position", JailTime: "3 minutes", Explanation: "A government employee who acts outside the interests of the public good or public justice."},
					{Name: "Dissuading a witness or a victim", JailTime: "3 minutes", Explanation: "A person who knowingly and maliciously prevents or encourages a witness or victim from giving a testimony."},
					{Name: "Distribution of illegal firearms", JailTime: "4 minutes", Explanation: "Selling or giving away illegal firearms."},
					{Name: "Extortion", JailTime: "3 minutes 30 seconds", Explanation: "A person who intimidates or influences to provide or hand over properties or services."},
					{Name: "Escape", JailTime: "4 minutes", Explanation: "A person who has been detained and arrested, booked, charged, or convicted of any crime who escapes from custody."},
					{Name: "Accessory to escape", JailTime: "3 minutes", Explanation: "A person that helps or plans someone who has been detained and arrested, booked, charged or convicted of any crime who escapes from custody."},
					{Name: "Felony drug possession", JailTime: "Class A: 3 min, Class B: 2 min, Class C: 1 min", Explanation: "Possession of a illegal substance one pound or more."},
					{Name: "Fleeing & Eluding", JailTime: "3 minutes 30 seconds", Explanation: "A person who runs away from police in a vehicle or on foot to elude arrest or questioning."},
					{Name: "Hit & Run", JailTime: "2 minutes 30 seconds", Explanation: "Leaving the scene of an accident."},
					{Name: "Grand Theft Auto", JailTime: "3 minutes", Explanation: "A person who has stolen a motor vehicle."},
					{Name: "Perjury", JailTime: "2 minutes", Explanation: "A person who knowingly provides false information while under oath in a court of law."},
					{Name: "Prostitution", JailTime: "2 minutes", Explanation: "A person who exchanges sexual acts for payment or other goods."},
					{Name: "Pandering / Pimping", JailTime: "2 minutes 30 seconds", Explanation: "A person who solicits or advertises and supervises anyone involved in prostitution."},
					{Name: "Possession of illegal firearm Class A", JailTime: "2 minutes 30 seconds", Explanation: "A person who was in possession of a illegal pistol or handgun."},
					{Name: "Possession of illegal firearm Class B", JailTime: "3 minutes", Explanation: "A person who was in possession of a illegal shotgun."},
					{Name: "Possession of illegal firearm Class C", JailTime: "4 minutes", Explanation: "A person who was in possession of a illegal fully automatic firearm."},
					{Name: "Improper use of a motor vehicle", JailTime: "2 minutes", Explanation: "A person who excessively speeds and is driving recklessly."},
					{Name: "Manslaughter", JailTime: "3 minutes", Explanation: "A person who unintentionally kills another."},
					{Name: "Police Impersonation", JailTime: "3 minutes", Explanation: "A person who pretends or implies the role of a police officer."},
					{Name: "Kidnapping", JailTime: "3 minutes 30 seconds", Explanation: "A person who detains or arrests another without their consent and unlawfully."},
					{Name: "Murder", JailTime: "5 minutes", Explanation: "A person who unlawfully kills another."},
					{Name: "Accessory to murder", JailTime: "4 minutes 30 seconds", Explanation: "A person who helps or plans to unlawfully kills another."},
					{Name: "Murder of a LEO", JailTime: "6 minutes", Explanation: "A person who unlawfully kills a LEO."},
					{Name: "Accessory to murder of a LEO", JailTime: "5 minutes", Explanation: "A person who helps or plans the unlawfully kills of a LEO."},
					{Name: "Conspiracy to commit murder", JailTime: "3 minutes 30 seconds", Explanation: "Planning to or discussing to commit murder."},
					{Name: "Solicitation of murder", JailTime: "4 minutes", Explanation: "Paying to have an individual(s) killed."},
					{Name: "Sexual battery", JailTime: "3 minutes", Explanation: "A person who commits unwanted touching or sexual contact."},
					{Name: "Resisting Arrest", JailTime: "4 minutes", Explanation: "A person who avoids apprehension from an officer by any means."},
					{Name: "Rape", JailTime: "4 minutes 30 seconds", Explanation: "A person who forces another to engage in sexual activities."},
					{Name: "Trespassing within a restricted facility", JailTime: "3 minutes", Explanation: "A person who, without proper authorization enters any government owned or managed facility that is secured with the intent of keeping people out."},
					{Name: "Terrorism", JailTime: "10 minutes", Explanation: "A person who uses threats or actions against the public to cause fear at a grand scale."},
					{Name: "Vigilantism", JailTime: "4 minutes", Explanation: "A person who attempts to effect justice according to their own understanding of right and wrong."},
					{Name: "Possession of an illegal weapon (Knife)", JailTime: "3 minutes", Explanation: "A person who is in possession of a blade over three inches."},
				},
			},
			{
				ID:       "other",
				Name:     "Other",
				Subtitle: "",
				Icon:     "fa-circle-exclamation",
				Color:    "#8b5cf6",
				Columns:  []string{"name", "jailTime"},
				Violations: []models.PenalCodeViolation{
					{Name: "Cop/Civ baiting", JailTime: "1st offense: 5 min jail. 2nd offense: 10 min jail. 3rd offense: 15 min jail. If anybody continues to Cop/Civ bait after 3 offenses you will be contacted by an Admin."},
					{Name: "MetaGaming", JailTime: "1st offense: 5 min jail. 2nd offense: 10 min jail. 3rd offense: 15 min jail. If anybody continues to MetaGame after 3 offenses you will be contacted by an Admin."},
				},
			},
		},
	}
}
