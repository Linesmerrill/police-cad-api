// MongoDB Index Creation Script — Configurable Forms System
//
// Run this in MongoDB Atlas → your cluster → "Browse Collections" → "Shell" tab,
// OR via mongosh:
//   mongosh "YOUR_CONNECTION_STRING" < create_indexes_configurable_forms.js
//
// Idempotent — safe to run multiple times.

function createIndexSafe(collection, key, options) {
  const keyStr = JSON.stringify(key);
  // getIndexes() throws "ns does not exist" on collections that haven't
  // been created yet. Treat that as "no indexes exist" and proceed —
  // createIndex will create the collection.
  let indexes = [];
  try {
    indexes = collection.getIndexes();
  } catch (e) {
    if (!(e.code === 26 || e.codeName === "NamespaceNotFound" || (e.message || "").includes("ns does not exist"))) {
      print(`❌ Could not read existing indexes for ${collection.getName()}: ${e.message}`);
      return;
    }
    // Otherwise: collection doesn't exist yet — fall through and create.
  }
  const exists = indexes.some(idx => JSON.stringify(idx.key) === keyStr);
  if (exists) {
    const existingIdx = indexes.find(idx => JSON.stringify(idx.key) === keyStr);
    print(`⚠️  Index already exists: ${existingIdx.name} (skipping ${options.name || 'unnamed'})`);
    return;
  }
  try {
    collection.createIndex(key, options);
    print(`✓ Created index: ${options.name || 'unnamed'} on ${collection.getName()}`);
  } catch (e) {
    if (e.code === 85 || e.message.includes("already exists") || e.message.includes("IndexOptionsConflict")) {
      print(`⚠️  Index already exists (different name): ${options.name || 'unnamed'} - skipping`);
    } else if (e.code === 11000 || e.message.includes("duplicate key")) {
      print(`⚠️  Cannot create unique index ${options.name || 'unnamed'}: duplicate keys found.`);
      print(`   Clean up duplicates first or drop the unique constraint.`);
    } else {
      print(`❌ Error creating index ${options.name || 'unnamed'}: ${e.message}`);
    }
  }
}

print("=== Configurable Forms — Index Creation ===");

// formTemplates: lookup by community + slug must be unique so a community
// can't have two templates with the same slug.
createIndexSafe(
  db.formTemplates,
  { "formTemplate.communityID": 1, "formTemplate.slug": 1 },
  {
    name: "formTemplate_community_slug_unique",
    unique: true,
    background: true,
  }
);

// formTemplateVersions: most reads are by (formTemplateID, version) for
// hydrating a template at its current version.
createIndexSafe(
  db.formTemplateVersions,
  { "formTemplateVersion.formTemplateID": 1, "formTemplateVersion.version": -1 },
  {
    name: "formTemplateVersion_template_version_idx",
    background: true,
  }
);

// departmentFormToggles: lookup by community + department + slug must be
// unique so the upsert path stays atomic.
createIndexSafe(
  db.departmentFormToggles,
  {
    "departmentFormToggle.communityID": 1,
    "departmentFormToggle.departmentId": 1,
    "departmentFormToggle.formTemplateSlug": 1,
  },
  {
    name: "departmentFormToggle_unique",
    unique: true,
    background: true,
  }
);

// formSubmissions: report number must be unique within a community.
createIndexSafe(
  db.formSubmissions,
  { "formSubmission.communityID": 1, "formSubmission.reportNumber": 1 },
  {
    name: "formSubmission_community_reportNumber_unique",
    unique: true,
    background: true,
  }
);

// formSubmissions: paginated list-by-community filtered by template slug.
createIndexSafe(
  db.formSubmissions,
  {
    "formSubmission.communityID": 1,
    "formSubmission.formTemplateSlug": 1,
    "formSubmission.createdAt": -1,
  },
  {
    name: "formSubmission_community_slug_createdAt_idx",
    background: true,
  }
);

// formSubmissions: department-scoped list.
createIndexSafe(
  db.formSubmissions,
  { "formSubmission.communityID": 1, "formSubmission.departmentId": 1, "formSubmission.createdAt": -1 },
  {
    name: "formSubmission_community_department_createdAt_idx",
    background: true,
  }
);

// formSubmissions: lookup-by-link is the index that makes
// civilian/vehicle/firearm/citation profile pages fast.
createIndexSafe(
  db.formSubmissions,
  { "formSubmission.links.type": 1, "formSubmission.links.id": 1 },
  {
    name: "formSubmission_links_type_id_idx",
    background: true,
  }
);

// formSubmissions: officer-scoped list.
createIndexSafe(
  db.formSubmissions,
  { "formSubmission.signedBy.userID": 1, "formSubmission.createdAt": -1 },
  {
    name: "formSubmission_signedBy_user_createdAt_idx",
    background: true,
  }
);

// formCounters: atomic FindOneAndUpdate filter — must be unique to avoid
// counter races across replicas.
createIndexSafe(
  db.formCounters,
  { "communityID": 1, "slug": 1, "year": 1 },
  {
    name: "formCounter_unique",
    unique: true,
    background: true,
  }
);

print("=== Done. Run check_indexes.js to verify, or db.<collection>.getIndexes() ===");
