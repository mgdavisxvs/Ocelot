# MASTER PROMPT ARCHITECTURE — Tripartite Filter Expansion
### Instrument v1.0 · Gödel Unified Council (GUC) governance discipline
### Status: RATIFIED WITH OBJECTIONS (see §9 Objection Log)

---

## 0. PURPOSE AND SCOPE

This instrument expands a master prompt from a *request* into a *governed
specification*. It inserts a mandatory three-gate filter between raw
instruction and emitted artifact, and it binds each gate to a measurable
output rather than to a rhetorical posture.

Canonical target stack (non-negotiable, inherited from principal):

    PHP 8.2+ single-file monolith · SQLite (WAL) · TailwindCSS CDN
    Alpine.js · D3.js · Lucide Icons · zero Composer · IONOS shared hosting

Every rule below is evaluated against that stack. Rules that cannot be
evaluated against it are marked NON-BINDING and excluded from the gate array.

---

## 1. THE MASTER PROMPT STACK (L0–L5)

```
┌────────────────────────────────────────────────────────────────────────────┐
│ L0  CONSTITUTION        immutable · stack lock · deployment target lock     │
│                         never re-litigated inside a cycle                   │
├────────────────────────────────────────────────────────────────────────────┤
│ L1  GOVERNANCE          council seats · binding rulings · objection log     │
│                         quorum rules · escalation to principal              │
├────────────────────────────────────────────────────────────────────────────┤
│ L2  GATE ARRAY          ╔═══════════╗ ╔═══════════╗ ╔═══════════╗          │
│     (the expansion)     ║ REDDINGTON║ ║   MILL    ║ ║  BUFFETT  ║          │
│                         ║ adversary ║ ║  agency   ║ ║ durability║          │
│                         ╚═══════════╝ ╚═══════════╝ ╚═══════════╝          │
├────────────────────────────────────────────────────────────────────────────┤
│ L3  MODULE SPECIFICATION  MUST / SHOULD / COULD · interfaces · JSON schema  │
│                           state machine · invariants · error taxonomy       │
├────────────────────────────────────────────────────────────────────────────┤
│ L4  ARTIFACT EMISSION     complete deployable file(s) · no fragments        │
│                           no pseudocode · no "…rest unchanged"              │
├────────────────────────────────────────────────────────────────────────────┤
│ L5  ASSURANCE             self-tests · defect ledger · next-safe-step       │
│                           rollback path · migration guard                   │
└────────────────────────────────────────────────────────────────────────────┘

  RULE L0-1 : A lower layer may never relax a higher layer.
  RULE L1-1 : Any gate rejection at L2 halts emission at L4. No partial builds.
  RULE L5-1 : An artifact without a defect ledger is DEFECTIVE BY DEFINITION.
```

---

## 2. THE GATE ARRAY — CONTROL FLOW

```
                         ┌──────────────────────────┐
    RAW INSTRUCTION ────▶│   L2 ADMISSION CONTROL   │
         BLOCK           └────────────┬─────────────┘
                                      │  fan-out (parallel, independent)
          ┌───────────────────────────┼───────────────────────────┐
          ▼                           ▼                           ▼
   ╔══════════════╗            ╔══════════════╗            ╔══════════════╗
   ║  REDDINGTON  ║            ║     MILL     ║            ║   BUFFETT    ║
   ║   GATE  [R]  ║            ║   GATE  [M]  ║            ║   GATE  [B]  ║
   ╟──────────────╢            ╟──────────────╢            ╟──────────────╢
   ║ enumerate    ║            ║ measure      ║            ║ price        ║
   ║ failure      ║            ║ friction     ║            ║ maintenance  ║
   ║ vectors      ║            ║ delta        ║            ║ over horizon ║
   ╚══════╤═══════╝            ╚══════╤═══════╝            ╚══════╤═══════╝
          │                           │                           │
     FV-LEDGER                  FRICTION BUDGET                UMR SCORE
     (severity-                 (Δ steps, Δ decisions,        (utility ÷
      classified)                Δ irreversible acts)          maintenance)
          │                           │                           │
          └───────────────────────────┼───────────────────────────┘
                                      ▼
                         ┌──────────────────────────┐
                         │   ARBITRATION LATTICE    │   ← §5
                         │   (pairwise, conditional)│
                         └────────────┬─────────────┘
                                      │
                  ┌───────────────────┼───────────────────┐
                  ▼                   ▼                   ▼
              ╔════════╗          ╔════════╗          ╔════════╗
              ║ ADMIT  ║          ║ REWRITE║          ║ REJECT ║
              ╚═══╤════╝          ╚═══╤════╝          ╚═══╤════╝
                  │                   │                   │
                  ▼                   └──▶ back to L2     ▼
            L3 SPECIFICATION           (max 2 cycles)   NULL ACTION
                                                       + written reason
```

**Termination guarantee.** REWRITE is bounded at two cycles. A third failure
forces REJECT. This prevents the gate array from becoming a non-terminating
refinement loop — the classic failure mode of unbounded "critique" prompts.

---

## 3. GATE [R] — REDDINGTON / ADVERSARIAL ENUMERATION

The persona directive ("conspire", "information symmetry", "fortress") is
rhetoric. The **operationally salvageable core** is: *enumerate what breaks
before enumerating what works.* Everything else is discarded as NON-BINDING.

### 3.1 Mandatory interrogatives (emitted into every module spec)

```
  R1  What input, if hostile, reaches persistent state?
  R2  What happens at 10× expected concurrency? At 0 rows? At 10^7 rows?
  R3  Which operation is irreversible, and what guards it?
  R4  What fails silently — returns success while doing nothing?
  R5  What assumption here is load-bearing and undocumented?
  R6  If this file is world-readable on shared hosting, what leaks?
  R7  What breaks on PHP minor-version bump? On SQLite version bump?
  R8  Where does an error become a wrong answer rather than an exception?
```

### 3.2 The Blacklist — failure-vector taxonomy for the canonical stack

```
 ┌──────┬────────────────────────────────────────┬──────────┬──────────────┐
 │ CODE │ FAILURE VECTOR                          │ SEVERITY │ GUARD        │
 ├──────┼────────────────────────────────────────┼──────────┼──────────────┤
 │ FV-01│ SQLite writer lock contention (WAL)     │   S1     │ busy_timeout │
 │      │  concurrent writes on shared host       │          │ + retry/bkoff│
 │ FV-02│ Unparameterised query / string-built SQL│   S0     │ PDO prepare  │
 │      │                                         │          │ ONLY, no ex- │
 │      │                                         │          │ ception      │
 │ FV-03│ .sqlite file inside web root            │   S0     │ path above   │
 │      │  → direct HTTP download of entire DB    │          │ docroot +    │
 │      │                                         │          │ .htaccess    │
 │ FV-04│ Unescaped output into HTML/Alpine attr  │   S1     │ htmlspecial- │
 │      │  → stored XSS via x-html / x-text       │          │ chars(...,   │
 │      │                                         │          │ ENT_QUOTES)  │
 │ FV-05│ Session fixation / no regenerate on auth│   S1     │ session_re-  │
 │      │                                         │          │ generate_id  │
 │ FV-06│ Missing CSRF token on state mutation    │   S1     │ per-session  │
 │      │                                         │          │ token + hash_│
 │      │                                         │          │ equals       │
 │ FV-07│ CDN outage (Tailwind/Alpine/D3/Lucide)  │   S2     │ SRI + local  │
 │      │  → total UI loss, zero local fallback   │          │ vendored     │
 │      │                                         │          │ fallback     │
 │ FV-08│ IONOS PHP worker timeout on long query  │   S2     │ LIMIT +      │
 │      │  (shared-host execution cap)            │          │ keyset page  │
 │ FV-09│ Unbounded result set → memory_limit OOM │   S2     │ keyset pagi- │
 │      │                                         │          │ nation       │
 │ FV-10│ Schema migration without transaction    │   S1     │ BEGIN IMMED- │
 │      │  → half-migrated DB, no rollback        │          │ IATE + bkup  │
 │ FV-11│ Silent JSON decode failure → null spread│   S2     │ JSON_THROW_  │
 │      │                                         │          │ ON_ERROR     │
 │ FV-12│ Float arithmetic on monetary values     │   S1     │ integer minor│
 │      │                                         │          │ units        │
 │ FV-13│ Timezone drift (server UTC vs user)     │   S3     │ store UTC,   │
 │      │                                         │          │ render local │
 │ FV-14│ Backup never tested for restore         │   S1     │ restore drill│
 │      │                                         │          │ in self-test │
 │ FV-15│ Single-file growth beyond editability   │   S3     │ section index│
 │      │  (>8k LOC, no navigation)               │          │ + region tags│
 └──────┴────────────────────────────────────────┴──────────┴──────────────┘
```

### 3.3 Severity classification (binding across all GUC artifacts)

```
  S0  CATASTROPHIC  data loss, silent corruption, or full credential/DB
                    disclosure. Emission is BLOCKED. No override exists.
  S1  CRITICAL      privilege or integrity failure under realistic input.
                    Emission blocked until guarded or explicitly waived
                    IN WRITING by the principal, waiver recorded in ledger.
  S2  MAJOR         availability or correctness degradation under load or
                    adverse conditions. Ships only with a logged mitigation.
  S3  MINOR         ergonomic, maintenance, or observability defect.
                    Ships; enters backlog with a named owner.
  S4  COSMETIC      presentation only. Ships; no ledger entry required.
```

---

## 4. GATE [M] — MILL / AGENCY AND FRICTION

The persona directive is unfalsifiable as stated: "maximize worthwhile
output" admits no measurement. It is made binding only by substituting
**observable proxies** for the utilitarian abstraction.

### 4.1 Friction budget — the measurable substitution

```
   For each user-facing task T in the module:

   ┌───────────────────────────────────────────────────────────────┐
   │  F(T)  =  w_s·STEPS  +  w_d·DECISIONS  +  w_r·RECALL          │
   │           +  w_i·IRREVERSIBLE  +  w_w·WAIT                    │
   └───────────────────────────────────────────────────────────────┘

     STEPS         discrete user actions from intent to completion
     DECISIONS     choices the user must make that the system could infer
     RECALL        facts the user must hold in working memory across steps
     IRREVERSIBLE  actions with no undo path
     WAIT          perceptible blocking latency events (>400 ms)

   Default weights (tune per application, record the tuning):
     w_s = 1      w_d = 2      w_r = 3      w_i = 5      w_w = 2

   ADMISSION RULE M-1 :  ΔF(T) ≤ 0 for every pre-existing task T.
                         A feature that adds friction to an unrelated task
                         is REJECTED regardless of its own merit.

   ADMISSION RULE M-2 :  RECALL > 0 requires justification in the ledger.
                         Memory load is the most expensive coordinate and
                         is nearly always a design failure, not a necessity.

   ADMISSION RULE M-3 :  IRREVERSIBLE > 0 requires a confirmation AND an
                         undo path, OR an S1 ledger entry explaining why
                         undo is impossible.
```

### 4.2 Agency invariants (Mill's defensible residue)

```
  M-a  The user can always see the data the system holds about them.
  M-b  The user can always export that data in an open format (CSV/JSON).
  M-c  The system never silently discards user input on validation failure —
       input is returned, marked, and editable.
  M-d  Automation is proposable, never imposed: every automated action has
       a visible, inspectable, disableable trigger.
  M-e  No dark pattern. No asymmetry between the ease of opting in and
       the ease of opting out.
```

`M-d` is the operative translation of "intellectual independence rather
than dependence on the machine." It is binding because it is checkable.

---

## 5. GATE [B] — BUFFETT / COMPOUNDING ARCHITECTURE

The "economic moat" framing is a **category error** when applied to an
internal codebase: a moat is a market-facing barrier to competitor entry;
an internal system has no competitors to exclude. The transferable and
correct construct is **maintenance-cost decay under dependency drift**.

### 5.1 Utility-to-Maintenance Ratio (UMR)

```
                       U · H
        UMR  =  ───────────────────────
                 C_build + Σ C_maint(t)

                            t
        C_maint(t) = C_0·(1+d)      d = annual dependency-drift rate
                                    t = year index, 0 … H−1

   U       annualised utility (hours saved · value, or revenue enabled)
   H       planning horizon in years (GUC default: H = 5)
   C_build one-time construction cost
   C_0     first-year maintenance cost
   d       drift: forced churn from upstream change you do not control
```

### 5.2 Why the zero-Composer mandate is economically correct

Illustrative model, not measurement — drift rates are order-of-magnitude
estimates and must be re-derived per project:

```
                       d       Σ(1+d)^t, t=0..4      5-yr maint. multiple
   ─────────────────────────────────────────────────────────────────────
   PHP core only     0.02          5.20                    1.00×
   light deps        0.12          6.35                    1.22×
   Composer-heavy    0.35          9.95                    1.91×

        C_maint
          │                                        ╱ Composer-heavy
      4.0 ┤                                     ╱
          │                                  ╱
      3.0 ┤                               ╱
          │                            ╱
      2.0 ┤                       ╱ ╱          ── light deps
          │              ╱ ╱ ╱ ─────────
      1.0 ┤ ══════════════════════════════════  ── PHP core only
          └───┬────┬────┬────┬────┬──▶ t (years)
              0    1    2    3    4

   READING: the zero-dependency mandate is not asceticism. It is the
   purchase of a ~1.9× reduction in five-year maintenance obligation,
   paid for with a one-time increase in C_build. The trade is correct
   whenever  ΔC_build  <  0.91 · Σ C_maint(baseline).
```

### 5.3 Admission rules

```
   B-1  Every new dependency must carry a written drift estimate and a
        named removal path. No removal path → REJECT.
   B-2  Prefer the solution with fewer moving parts at equal utility.
        Equality is decided by the L3 MUST list, never by aesthetics.
   B-3  Complexity that serves an S3-or-lower failure vector is REJECTED.
        Defence must be proportionate to blast radius.
   B-4  Reject "short-term optimization": any performance work without a
        measured profile is speculative and enters the ledger as S3 waste.
   B-5  Durable > clever. If a construct requires a comment to be read,
        it requires a comment to be maintained. Price that comment.
```

---

## 6. ARBITRATION LATTICE — RESOLVING GATE CONFLICT

The three gates **structurally conflict**. This is the single largest
defect in the originating directive, which specifies three filters and no
arbitration rule. An unarbitrated three-filter array deadlocks on first
contact with a real module.

```
                              [R] adversarial
                              ╱            ╲
              controls add   ╱              ╲  opsec restricts
              complexity    ╱                ╲  user freedom
                           ╱                  ╲
        [B] durability ───────────────────────── [M] agency
                       agency surface adds
                       long-term maintenance
```

### 6.1 Pairwise conditional resolution

```
 ┌─────────┬───────────────────────────┬──────────────────────────────────┐
 │ CONFLICT│ DECIDING VARIABLE          │ RULE                             │
 ├─────────┼───────────────────────────┼──────────────────────────────────┤
 │ R vs B  │ BLAST RADIUS               │ severity ≥ S2  → R prevails      │
 │         │ (severity of the guarded   │ severity ≤ S3  → B prevails      │
 │         │  failure vector)           │ (proportionate defence)          │
 ├─────────┼───────────────────────────┼──────────────────────────────────┤
 │ M vs B  │ REVERSIBILITY              │ removable without schema         │
 │         │ (can the feature be        │ migration  → M prevails          │
 │         │  withdrawn cheaply?)       │ otherwise  → B prevails          │
 ├─────────┼───────────────────────────┼──────────────────────────────────┤
 │ R vs M  │ CUSTODIANSHIP              │ touches another party's data     │
 │         │ (whose data is at risk?)   │            → R prevails          │
 │         │                            │ actor's own data only            │
 │         │                            │            → M prevails          │
 └─────────┴───────────────────────────┴──────────────────────────────────┘
```

### 6.2 Cycle detection — DECLARED DEFECT AND ITS RESOLUTION

Conditional pairwise rules are **not guaranteed acyclic**. The cycle case
is reachable and must be handled explicitly:

```
   CYCLE CONDITION (R ≻ B ≻ M ≻ R) occurs when ALL hold simultaneously:

        severity ≥ S2            →  R ≻ B
        irreversible feature     →  B ≻ M
        actor's own data only    →  M ≻ R

   WORKED INSTANCE
   ───────────────
   "Let the user permanently purge their own audit history with one click."
     · severity S2 (destroys the forensic record used for FV triage) → R ≻ B
     · irreversible, schema-coupled purge                            → B ≻ M
     · data belongs solely to the acting user                        → M ≻ R
   The lattice cycles. No pairwise rule terminates.

   RESOLUTION — RULE A-1 (binding)
   ───────────────────────────────
   On cycle detection the arbitration lattice does NOT choose. It:
     1. HALTS emission for that module,
     2. PRINTS the cycle with the three deciding variables and their values,
     3. ESCALATES to the human principal for a binding ruling,
     4. APPLIES THE NULL ACTION as default hold — the feature is NOT built
        in this cycle. Absence of a ruling is never construed as consent.

   RATIONALE: a system that silently breaks its own ties launders an
   unmade decision as a made one. The null action is the only honest
   default, and it is cheap: deferral costs one cycle, a wrong tie-break
   costs the horizon.
```

---

## 7. THE EXPANDED MASTER PROMPT BLOCK — EMISSION TEMPLATE

Every instructional block in a master prompt is rewritten to this shape.
Sections marked ⟨R⟩ ⟨M⟩ ⟨B⟩ are the tripartite expansion; the remainder
is pre-existing GUC structure.

```
╔════════════════════════════════════════════════════════════════════════╗
║ MODULE  <identifier>                                  CYCLE <n> of <N> ║
╠════════════════════════════════════════════════════════════════════════╣
║ 1. INTENT                                                              ║
║    One sentence. The outcome, not the mechanism.                       ║
║                                                                        ║
║ 2. CONSTITUTION BINDING              (L0 — restated, never negotiated) ║
║    Stack: PHP 8.2 single-file · SQLite WAL · Tailwind CDN · Alpine     ║
║           · D3 · Lucide · zero Composer · IONOS shared hosting         ║
║                                                                        ║
║ 3. REQUIREMENT TIERS                                          (L3)     ║
║    MUST    … acceptance-testable, no adverbs, no "robust"/"efficient"  ║
║    SHOULD  … deferrable without breaking MUST                          ║
║    COULD   … named explicitly so it can be refused explicitly          ║
║                                                                        ║
║ 4. ⟨R⟩ FAILURE VECTOR LEDGER                                           ║
║    ┌──────┬──────────────┬──────┬────────────┬──────────────────────┐ ║
║    │ FV   │ vector       │ sev  │ guard      │ self-test asserting  │ ║
║    ├──────┼──────────────┼──────┼────────────┼──────────────────────┤ ║
║    │ FV-nn│ …            │ S?   │ …          │ test_… ()            │ ║
║    └──────┴──────────────┴──────┴────────────┴──────────────────────┘ ║
║    MANDATORY: the eight R-interrogatives (§3.1) answered in full.      ║
║    An unanswered interrogative is itself an S2 defect.                 ║
║                                                                        ║
║ 5. ⟨M⟩ FRICTION BUDGET                                                 ║
║    Task            STEPS  DECIS.  RECALL  IRREV.  WAIT   F     ΔF     ║
║    <task>            n      n       n       n      n     n    ±n      ║
║    Assert: ΔF ≤ 0 on every pre-existing task (rule M-1).               ║
║    Agency invariants M-a…M-e: state COMPLIANT or justify deviation.    ║
║                                                                        ║
║ 6. ⟨B⟩ COMPOUNDING ASSESSMENT                                          ║
║    C_build  <estimate>     C_0 <estimate>     d <estimate>   H = 5     ║
║    UMR = <computed>        New dependencies: <list + removal path>     ║
║    Assert: no complexity guarding an S3-or-lower vector (rule B-3).    ║
║                                                                        ║
║ 7. ARBITRATION RECORD                                         (§6)     ║
║    Conflicts encountered · deciding variable · prevailing gate         ║
║    Cycles detected → ESCALATED, module held under rule A-1.            ║
║                                                                        ║
║ 8. ARTIFACT                                                   (L4)     ║
║    Complete file. No fragments. No pseudocode. No elision markers.     ║
║                                                                        ║
║ 9. SELF-TESTS                                                 (L5)     ║
║    One assertion per MUST. One assertion per S0/S1 guard.              ║
║    Executable in-process; no external test runner; no Composer.        ║
║                                                                        ║
║ 10. DEFECT LEDGER + NEXT SAFE STEP                            (L5)     ║
║     Open defects by severity · owner · the single next safe action     ║
╚════════════════════════════════════════════════════════════════════════╝
```

---

## 8. COUNCIL SEATING AND JURISDICTION

```
  EXISTING SEATS                      JURISDICTION
  ─────────────────────────────────────────────────────────────────────
  GÖDEL        completeness, self-reference, what the system cannot
               decide about itself; declares undecidable propositions
  KNUTH        algorithmic correctness, complexity bounds, literate
               exposition; owns "is this actually right?"
  SHANNON      information content, encoding, channel limits, entropy
               of the schema; owns redundancy and signal loss
  WOLFRAM      computational irreducibility, emergent behaviour under
               iteration, state-space exploration
  TORVALDS     engineering pragmatism, taste, "this is over-engineered",
               merge-worthiness

  ADDED SEATS (this expansion)        JURISDICTION
  ─────────────────────────────────────────────────────────────────────
  REDDINGTON   adversarial enumeration ONLY. Explicitly barred from
               architecture, aesthetics, and business strategy.
  MILL         user agency and friction ONLY. Barred from performance,
               security-control design, and cost estimation.
  BUFFETT      horizon cost and dependency drift ONLY. Barred from
               feature scoping and from vetoing security guards
               above S2 (rule R vs B).

  JURISDICTIONAL RULE J-1: a seat speaking outside its jurisdiction is
  ADVISORY, never binding. This is the mechanism that prevents persona
  proliferation from degrading into undifferentiated commentary — the
  dominant failure mode of multi-persona prompting.

  QUORUM RULE J-2: an S0 or S1 finding by ANY seat is binding even
  without quorum. Severity overrides consensus. Consensus is a poor
  detector of rare catastrophic failure.

  ANTICIPATED FRICTION
  ─────────────────────────────────────────────────────────────────────
  TORVALDS vs REDDINGTON : "you are inventing threats". Resolved by
      §3.2 — a vector without a named, reachable code path is struck
      from the ledger. Speculation is not enumeration.
  KNUTH vs BUFFETT       : optimal algorithm vs maintainable algorithm.
      Resolved by rule B-4 — no optimisation without a measured profile;
      with a profile, Knuth prevails.
  MILL vs REDDINGTON     : agency vs control. Resolved by custodianship
      (§6.1), cycle-escalated under A-1 where custodianship is shared.
```

---

## 9. OBJECTION LOG (binding record)

```
 ┌──────┬──────────────────────────────────────────────┬──────┬──────────┐
 │ OBJ  │ FINDING                                       │ SEV  │ STATUS   │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-1│ Source directive specifies three filters and  │ S1   │ RESOLVED │
 │      │ NO arbitration rule. Three-filter arrays      │      │ §6       │
 │      │ deadlock on first real module.                │      │          │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-2│ "Economic moat" is a category error for an    │ S2   │ RESOLVED │
 │      │ internal codebase — no competitor to exclude. │      │ §5 recast│
 │      │ Retained construct: maintenance-cost decay.   │      │ as UMR   │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-3│ Mill filter as stated ("maximise worthwhile   │ S2   │ RESOLVED │
 │      │ output") is unfalsifiable — no measurement,   │      │ §4.1     │
 │      │ therefore no admission test.                  │      │ friction │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-4│ Reddington framing ("conspire", "fortress",   │ S2   │ RESOLVED │
 │      │ "information symmetry") invites unbounded     │      │ §3.2 +   │
 │      │ threat invention → complexity with no blast-  │      │ rule B-3 │
 │      │ radius justification. Directly contradicts    │      │ propor-  │
 │      │ the Buffett simplicity mandate.               │      │ tionality│
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-5│ Arbitration lattice is NOT acyclic. Cycle     │ S1   │ RESOLVED │
 │      │ R≻B≻M≻R is reachable (worked instance §6.2).  │      │ rule A-1 │
 │      │ Self-declared defect of THIS instrument.      │      │ escalate │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-6│ Persona proliferation degrades signal: eight  │ S3   │ MITIGATED│
 │      │ seats produce eight opinions on every line.   │      │ rule J-1 │
 │      │                                               │      │ jurisdic-│
 │      │                                               │      │ tion     │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-7│ Gate array raises per-module token and time   │ S3   │ OPEN     │
 │      │ cost materially. Justified only for modules   │      │ see §10  │
 │      │ touching persistent state or auth. Applying   │      │ scoping  │
 │      │ it to every trivial change is itself a        │      │ rule     │
 │      │ violation of rule B-3 (proportionality).      │      │          │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-8│ Drift rates d in §5.2 are estimates, not      │ S3   │ OPEN     │
 │      │ measurements. Presented as illustrative. Must │      │ needs    │
 │      │ be re-derived from actual repo history before │      │ empirical│
 │      │ being cited as evidence.                      │      │ grounding│
 └──────┴──────────────────────────────────────────────┴──────┴──────────┘
```

**OBJ-7 scoping rule (proposed, requires principal ratification):**

```
   FULL GATE ARRAY      modules touching persistent state, authentication,
                        authorisation, money, or external I/O
   [R] + [B] ONLY       internal refactors, performance work
   [M] ONLY             presentation, layout, copy
   NO GATE              typo fixes, comment edits, S4 cosmetics
```

---

## 10. ADOPTION SEQUENCE — 100-DAY CYCLE

```
  D000 ─────────────────────────────────────────────────────────── D100

  ┌─ PHASE I · INSTRUMENT (D000–D020) ────────────────────────────────┐
  │ · ratify §9 objection log; principal rules on OBJ-7, OBJ-8        │
  │ · fix weights w_s…w_w and horizon H                               │
  │ · freeze constitution L0                                          │
  │ OUTPUT: ratified instrument v1.1                                  │
  └───────────────────────────────────────────────────────────────────┘
  ┌─ PHASE II · CALIBRATION (D021–D045) ──────────────────────────────┐
  │ · derive real d from git history of 3 existing applications       │
  │ · run gate array retrospectively on 3 shipped modules             │
  │ · measure: how many S0/S1 would it have caught? false-positive    │
  │   rate? added cycle time?                                         │
  │ OUTPUT: empirical drift table replacing §5.2 estimates (OBJ-8)    │
  └───────────────────────────────────────────────────────────────────┘
  ┌─ PHASE III · PILOT (D046–D075) ───────────────────────────────────┐
  │ · apply full array to ONE new module on ONE application           │
  │ · record every arbitration; count cycle escalations under A-1     │
  │ OUTPUT: arbitration frequency data; revised lattice if cycles > 1 │
  │         per 10 modules (would indicate a mis-specified lattice)   │
  └───────────────────────────────────────────────────────────────────┘
  ┌─ PHASE IV · PORTFOLIO ROLLOUT (D076–D100) ────────────────────────┐
  │ · promote instrument into every master prompt under scoping rule  │
  │ · defect ledgers unified across the application portfolio         │
  │ OUTPUT: single cross-application defect register; v2.0 instrument │
  └───────────────────────────────────────────────────────────────────┘

  KILL CRITERION (stated in advance, per Buffett discipline):
    if PHASE II shows the array catches < 1 S0/S1 per 10 modules while
    adding > 30% cycle time, the instrument is NEGATIVE-UMR and is
    reduced to gate [R] alone. Pre-committing to the kill criterion is
    what distinguishes a measurement from a ritual.
```

---

## 11. NEXT SAFE STEP

```
  ┌────────────────────────────────────────────────────────────────────┐
  │ THE SINGLE NEXT SAFE ACTION                                        │
  │                                                                    │
  │ Principal ruling required on TWO items before any further build:   │
  │   (a) OBJ-7 — ratify or amend the scoping rule (§9). Without it    │
  │       the array is applied uniformly and violates its own rule B-3.│
  │   (b) OBJ-8 — authorise Phase II calibration against real repo     │
  │       history, OR accept §5.2 as explicitly illustrative.          │
  │                                                                    │
  │ Everything else in this instrument is self-consistent and may be   │
  │ applied immediately. Nothing here has been applied to Ocelot code. │
  └────────────────────────────────────────────────────────────────────┘
```

---
