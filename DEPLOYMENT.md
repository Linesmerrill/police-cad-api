# Deployment Guide

## Critical: Procfile Requirements

⚠️ **IMPORTANT**: The `Procfile` is **REQUIRED** for Heroku deployment. Without it, the application will fail to start and cause a complete outage.

### Procfile Requirements

1. **Must exist**: The `Procfile` file must be present in the root directory
2. **Must contain web process**: Must have a `web:` process type defined
3. **Must be committed**: The Procfile must be committed to git and pushed to Heroku

### Current Procfile

```
web: bin/police-cad-api
```

### Validation

Before pushing to Heroku, always run:

```bash
make check-procfile
```

This will verify:
- ✅ Procfile exists
- ✅ Contains `web:` process type
- ✅ Points to correct binary: `bin/police-cad-api`

### Pre-Push Checklist

Before pushing to Heroku, run:

```bash
make pre-push
```

This runs:
1. Procfile validation
2. Mock generation
3. Tests

### CI/CD Protection

The GitHub Actions workflow automatically validates the Procfile on every push and pull request. If the Procfile is missing or invalid, the CI will fail and prevent merging.

### What Happens If Procfile Is Missing?

- ❌ Heroku cannot determine how to start the application
- ❌ No `web` process type available
- ❌ Error: `H14 - No web processes running`
- ❌ **Complete application outage**

### Emergency Recovery

If the Procfile is accidentally deleted or modified incorrectly:

1. **Immediately restore the Procfile**:
   ```bash
   echo "web: bin/police-cad-api" > Procfile
   ```

2. **Commit and push**:
   ```bash
   git add Procfile
   git commit -m "Restore Procfile"
   git push heroku main
   ```

3. **Scale web dyno**:
   ```bash
   heroku ps:scale web=1 --app police-cad-app-api
   ```

### Preventing Future Issues

1. ✅ Procfile is tracked in git (not in .gitignore)
2. ✅ CI/CD validates Procfile on every push
3. ✅ Makefile has `check-procfile` target
4. ✅ Pre-push hook validates Procfile
5. ✅ This documentation exists

### Never Delete or Modify Procfile Without:

- Understanding the consequences
- Having a backup
- Testing locally first
- Following this deployment guide

