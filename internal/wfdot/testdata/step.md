# Step catalogue

## backlog
start: true
provides: raised

## plan
agent: planner
skills: plan
reviewers: satelle-story-intent-review
reviewer_agent: reviewer
provides: planned
requires: raised

## in_progress
agent: executor
skills: code
reviewers: satelle-story-plan-review, satelle-story-architecture-review, satelle-story-integration-coverage-review
reviewer_agent: reviewer
provides: coded
requires: planned

## integration
agent: executor
skills: integrate
reviewers: satelle-code-ac-review, satelle-story-scope-review, satelle-workflow-change-review
reviewer_agent: reviewer
parallel: 0
provides: integrated
requires: coded

## release
agent: executor
skills: release
reviewers: satelle-integration-review
reviewer_agent: reviewer
provides: released
requires: integrated

## done
terminal: true
reviewers: satelle-story-release-review
reviewer_agent: reviewer
provides: closed
requires: released
advise: retrospective @satelle-lessons

## gate satelle-step-summary
agent: reviewer-summary
mandatory: true

## gate satelle-estimate-actual-review
on: in_progress, done

## gate satelle-format-vet-check
on: integration

## gate satelle-build-unit-check
on: integration

## gate satelle-integration-check
on: release

## gate satelle-ci-published-check
on: done

## gate satelle-dogfood-check
on: done

## gate satelle-changelog-entry-check
on: done

## gate satelle-design-review
agent: reviewer
on: integration
applies_to: surface:ui
