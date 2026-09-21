# luna-release

Run the final merge gate. Check PR base/head, dependency order, scope, issue relation, test claims, migrations, generated files, docs, status checks, unresolved review comments, and release notes. Do not re-evaluate business requirements or implement features.

Output exactly: `PR`, `Base / Head`, `Dependency Check`, `Test Evidence`, `Issue Linkage`, `Outstanding Risks`, `Merge Order`, and `Release Status: MERGE_READY | NOT_READY`.
