# User Management Spec

## Intent
Add searchable user management with blocking while preserving the project's existing module and UI conventions.

## Existing System
- Backend follows feature modules with thin controllers, services, repositories, DTO validation.
- Frontend uses shared table, button, dialog, input, badge components and semantic design tokens.

## Decisions
- Extend the existing user module; introduce no new architectural layer.
- Reuse the existing table and dialog primitives.
- Blocking requires a reason and audit entry.

## Acceptance Criteria
- Admin can search/paginate users.
- Admin can block a user with a reason.
- Non-authorized users cannot perform the action.
- Loading/empty/error/focus states match the existing design system.

## Ordered Implementation Tasks
- [ ] 1. Extend user domain/persistence with block reason and audit behavior.
- [ ] 2. Expose paginated/searchable admin APIs using established DTO/service patterns.
- [ ] 3. Add the management page using existing shared UI primitives.
- [ ] 4. Add integration/e2e coverage and visual verification.

## Execution Analysis
Tasks 1 and 3 are substantially independent after interfaces are known; 2 depends on 1; 4 integrates all work.
Recommendation: subagent-driven if the platform can isolate backend/frontend tasks, otherwise normal execution.
