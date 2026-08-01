---
name: step
type: workflow
scope: system
tags: [type:workflow]
description: Order-zero step catalogue every repo inherits — the steps and always-on gates a route selects from by obligation. A step declares what it provides, what it requires, who performs it and the reviewers gating ENTRY to it. Half of a derived route; done.md is the other half.
---

# Step catalogue

A step carries no executor rubric: the binary ships no opinion about how work is
done. A repo names one with `skills:` when it authors one.

## backlog
start: true
provides: raised

## in_progress
agent: executor
reviewers: satelle-story-intent-review
reviewer_agent: reviewer
provides: coded
requires: raised

## done
reviewers: satelle-workflow-change-review, satelle-story-scope-review, satelle-story-done-review
reviewer_agent: reviewer
parallel: 0
terminal: true
provides: closed
requires: coded

## done
agent: reviewer
reviewers: satelle-story-done-review
terminal: true
provides: children-resolved
requires: raised

## in_progress
agent: executor
reviewers: satelle-task-validate-before-review
reviewer_agent: reviewer
provides: run
requires: raised

## done
reviewers: satelle-task-validate-after-review
reviewer_agent: reviewer
terminal: true
provides: run-verified
requires: run

## gate satelle-step-summary
agent: reviewer
mandatory: true
for: *, epic-parent, parent

## gate satelle-estimate-actual-review
on: in_progress, done
for: *
