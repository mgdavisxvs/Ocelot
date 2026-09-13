# MASTER PROMPT ARCHITECTURE — Tripartite Filter Expansion
### Instrument v1.3 · Gödel Unified Council (GUC) governance discipline
### Status: RATIFIED. Ledger CLEAR — no S0/S1/S2 open (§12.2)
### Principal rulings of record: OBJ-7 (§9.1) · OBJ-8 (§11)

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

Illustrative model. The Phase II calibration (§11) attempted to replace
these estimates with measured values and FAILED to derive them — the
repository carries no usable dependency history. These figures therefore
remain EXPLICITLY ILLUSTRATIVE and may not be cited as evidence. §11 also
falsifies the continuous-decay form of C_maint used below; see rule B-6.

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
   B-6  (added v1.1, from §11) Maintenance is BURSTY, not continuous.
        Do not integrate drift over wall-clock time for a system that is
        frozen or unused. Index C_maint by ACTIVE years, and model
        reactivation as a STEP cost, not as accrued decay. A system that
        nobody runs costs nothing to not maintain.
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
 │ OBJ-8│ Drift rates d in §5.2 are estimates, not      │ S3   │ CALIBRA- │
 │      │ measurements.                                 │      │ TED §11  │
 │      │ RESULT: NOT DERIVABLE. n=0 version-change     │      │ retained │
 │      │ events; manifest lifetime 1 day. §5.2 stands  │      │ as ILLUS-│
 │      │ as illustrative and is barred from citation.  │      │ TRATIVE  │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │ OBJ-9│ C_maint(t)=C_0(1+d)^t assumes CONTINUOUS      │ S2   │ RATIFIED │
 │      │ decay. Repository evidence (10.8-year         │      │ v1.2 —   │
 │      │ dormancy at zero cost, then successful        │      │ activity-│
 │      │ resurrection) falsifies it. Maintenance is    │      │ indexed  │
 │      │ BURSTY and DEFERRABLE, not continuous.        │      │ form is  │
 │      │ Activity-indexed form + rule B-6 adopted.     │      │ binding  │
 ├──────┼──────────────────────────────────────────────┼──────┼──────────┤
 │OBJ-10│ FV-07 severity UNDERSTATED in v1.0. It was    │ S1   │ RESOLVED │
 │      │ classified S2 (availability). Unpinned third- │      │ FV-07    │
 │      │ party script in an authenticated or           │      │ split;   │
 │      │ credential-entry page is arbitrary code       │      │ see §12  │
 │      │ execution → S0. Self-inflicted defect of the  │      │          │
 │      │ instrument's own taxonomy.                    │      │          │
 └──────┴──────────────────────────────────────────────┴──────┴──────────┘
```

### 9.1 OBJ-7 RULING — AMENDED AND RATIFIED

The rule proposed in v1.0 was **rejected as drafted** and replaced. Three
defects were found in it, one of them confirmed empirically by §13.

```
 DEFECT OF THE PROPOSED RULE          CORRECTION IN THE RATIFIED RULE
 ────────────────────────────────────────────────────────────────────────
 D1  Classified by AUTHOR INTENT      Classify by DIFF TOUCH-SET. A tier
     ("this is a presentation         computed from what the diff touches
     change"). Self-classification    cannot be lowered by how the change
     is gameable and error-prone.     is described.
 D2  "[M] only" tier for presen-      T1 becomes [M] + [R-lite]. FV-04
     tation omits [R] entirely —      (stored XSS via x-html/x-text) is a
     but the stack's S1 XSS vector    PRESENTATION defect. An [M]-only
     lives precisely in presentation. tier is structurally blind to it.
 D3  No rule for diffs spanning       Rule A-2: tier = MAX over all
     tiers.                           triggers matched. Never the mode.
 ────────────────────────────────────────────────────────────────────────
 D4  CONFIRMED BY CALIBRATION: the proposed rule routed <script src="…">
     tags to T1 ([M] only) as "layout". That is the exact file position
     of the live S0 recorded in §12. The proposed rule would have MISSED
     the single most severe defect in the repository it governs.
     Correction: third-party script/style URLs are a T3 trigger.
```

**RATIFIED TIER RULE (binding, v1.1)**

```
 TIER │ TRIGGER — computed from the DIFF TOUCH-SET, never from intent │ GATES
 ─────┼───────────────────────────────────────────────────────────────┼──────
  T3  │ schema or migration · any SQL text · auth / session / token   │ FULL
      │ · authorisation check · monetary field · filesystem, network  │ ARRAY
      │ or process I/O · serialization boundary · cryptography        │ [R]
      │ · THIRD-PARTY SCRIPT OR STYLE URL            ◄── added by D4  │ [M]
      │                                                               │ [B]
 ─────┼───────────────────────────────────────────────────────────────┼──────
  T2  │ control flow · concurrency · caching · index · query shape     │ [R]
      │ with SQL semantics unchanged · build, deploy or CI config      │ [B]
 ─────┼───────────────────────────────────────────────────────────────┼──────
  T1  │ template · markup · CSS · user-visible copy                    │ [M]
      │ R-lite = interrogatives R1 and R6 ONLY (what hostile input     │  +
      │ reaches this output; what leaks if this is served)             │ R-lite
 ─────┼───────────────────────────────────────────────────────────────┼──────
  T0  │ comments · whitespace · prose in non-executable position       │ NONE
 ─────┴───────────────────────────────────────────────────────────────┴──────

  A-2  TIER = MAX over every trigger the diff matches. A diff touching
       both copy and a migration is T3, not T1 and not "mostly T1".
  A-3  ESCALATION is always permitted and needs no justification.
       DE-ESCALATION requires a written reason and is itself a ledger
       entry, reviewable. Asymmetry is deliberate: the cheap error is
       over-gating, the expensive error is under-gating.
  A-4  The tier is computed BEFORE the artifact is written, from the
       planned touch-set, and RECOMPUTED after. If the realised diff
       raises the tier, the module re-enters the gate array at the
       higher tier. Scope creep cannot silently lower assurance.
```

This rule satisfies rule B-3 (proportionality) — the array's cost now
tracks blast radius rather than being levied uniformly — while closing
the two blindnesses that made the v1.0 draft unsafe.

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

## 11. PHASE II CALIBRATION REPORT (OBJ-8 ruling of record)

**Authorised and executed against real repository history. Result: the
requested measurement is NOT DERIVABLE from this repository.** Reported
as a negative finding rather than substituted with plausible numbers.

### 11.1 What the history actually contains

```
  OBSERVATION WINDOW      2010-10-27 … 2026-09-13   (15.9 yr, 45 commits)

  2010 ──── C++ era, active ──── 2015-01-27
                                  │◄──── 10.8 yr DORMANCY ────►│
                                                          2025-11-17 ──── Go
                                                          port era ──── 2026

  MANIFEST                 FIRST SEEN    COMMITS TOUCHING    LIFETIME
  ──────────────────────────────────────────────────────────────────────
  go.mod                   2026-09-13           1             1 day
  go.sum                   2026-09-13           1             1 day
  markov/go.mod            2026-09-13           1             1 day
  markov/go.sum            2026-09-13           1             1 day

  VERSION-CHANGE EVENTS OBSERVED : 0
  DEPENDENCY UPGRADES OBSERVED   : 0
  DERIVABLE ANNUAL DRIFT RATE d  : NONE — n = 0
```

### 11.2 Ruling

```
  §5.2 drift table REMAINS EXPLICITLY ILLUSTRATIVE and is BARRED from
  citation as evidence in any ruling, estimate, or specification.

  The zero-Composer mandate stands on its constitutional (L0) footing —
  it is a principal's decision — NOT on the §5.2 numbers, which are
  unvalidated. Stating the distinction is the point: an unvalidated
  model that happens to endorse a decision you already made is the most
  dangerous kind of evidence, because it is indistinguishable from
  confirmation until someone checks it.

  RE-CALIBRATION PRECONDITION: d becomes derivable only from a repo with
  ≥ 3 years of manifest history and ≥ 10 observed version-change events.
  No repository in scope currently meets this. Until one does, the drift
  argument is a HYPOTHESIS, not a finding.
```

### 11.3 Unrequested finding — the model is wrong (OBJ-9)

The calibration failed at its stated task and succeeded at an unstated one.
The 10.8-year dormancy is direct evidence against the instrument's own
maintenance model:

```
  PREDICTED by C_maint(t) = C_0(1+d)^t over 2015→2025:
      continuous accrual; ~10 years of compounding maintenance obligation;
      codebase expected to be economically unrecoverable by 2025.

  OBSERVED:
      ZERO commits, ZERO maintenance cost incurred, and the codebase was
      then successfully resurrected and ported to Go in a bounded effort.

  CONCLUSION: the exponential-decay form is FALSIFIED for dormant systems.
      Maintenance cost is not a continuous function of elapsed time. It is
      a function of ACTIVITY, plus a STEP cost at reactivation.

  RATIFIED FORM (v1.2, binding — supersedes the §5.1 continuous form):
                                        τ(t)
        C_maint(t) = A(t) · C_0 · (1+d)        +  R · [reactivation event]

        A(t) ∈ {0,1}   activity indicator
        τ(t)           cumulative ACTIVE years, not elapsed years
        R              one-time reactivation cost (the Go port, here)

  PRACTICAL CONSEQUENCE: "freeze it" is a legitimate and cheap strategy
  that the v1.0 model could not express. Buffett's own discipline —
  inactivity as a position — was absent from the gate that bears his name.
```

---

## 12. LIVE DEFECT LEDGER — Ocelot admin panel

Produced incidentally by the §11 pass. Verified against source, not inferred.

```
 ┌──────┬───────────────────────────────────────────────┬──────┬──────────┐
 │ ID   │ DEFECT                                         │ SEV  │ EVIDENCE │
 ├──────┼───────────────────────────────────────────────┼──────┼──────────┤
 │ D-01 │ admin/login.php loads an UNPINNED third-party  │  S0  │ login.   │
 │      │ script (unpkg.com/lucide@latest, floating tag, │      │ php:32   │
 │      │ no SRI) on the page whose form posts the admin │      │ form :55 │
 │      │ password. A hijacked or compromised publish at │      │ pwd input│
 │      │ that tag executes arbitrary JS in the          │      │ :67      │
 │      │ credential-entry document → admin credential   │      │          │
 │      │ capture at keystroke time. Meets the S0        │      │          │
 │      │ definition: full credential disclosure.        │      │          │
 ├──────┼───────────────────────────────────────────────┼──────┼──────────┤
 │ D-02 │ Same pattern across the AUTHENTICATED panel:   │  S1  │ header.  │
 │      │ lucide@latest and alpinejs@3.x.x (both         │      │ php:25,  │
 │      │ floating), cdn.tailwindcss.com (unversioned),  │      │ :31, :9  │
 │      │ d3.v7.min.js (major-pinned only). Arbitrary JS │      │          │
 │      │ in an authenticated admin session → full DB    │      │          │
 │      │ read via the admin API surface.                │      │          │
 ├──────┼───────────────────────────────────────────────┼──────┼──────────┤
 │ D-03 │ ZERO integrity= (SRI) attributes anywhere in   │  S1  │ grep:    │
 │      │ admin/. Nothing detects a substituted payload. │      │ 0 hits   │
 ├──────┼───────────────────────────────────────────────┼──────┼──────────┤
 │ D-04 │ FV-07 (availability) confirmed live: four CDN  │  S2  │ 4 hosts, │
 │      │ origins, no local fallback. Any one outage →   │      │ 0 local  │
 │      │ total admin UI loss.                           │      │ vendored │
 └──────┴───────────────────────────────────────────────┴──────┴──────────┘

  NEGATIVE RESULT, recorded to avoid inflation:
    FV-04 (unescaped output) — header.php:6 emits $pageTitle unescaped,
    but $pageTitle is literal-assigned at five call sites and is NOT
    request-derived. NOT TRIGGERED. Logged as S3 latent: the construct
    becomes S1 the moment any caller assigns from $_GET/$_POST.

### 12.1 REMEDIATION — APPLIED AND VERIFIED (v1.2)

```
  Tier under the §9.1 ratified rule: T3 (third-party script URL + auth page)
  → FULL GATE ARRAY. Gate findings recorded below.

  ┌──────┬────────────────────────────────────────────────┬──────────────┐
  │ ID   │ REMEDIATION                                     │ VERIFIED BY  │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ D-01 │ login.php rebuilt with ZERO third-party         │ selftest     │
  │      │ resources: hand-written CSS replaces the        │ D-01 block   │
  │      │ Tailwind CDN, server-rendered inline SVG        │ (4 assertions│
  │      │ replaces the icon library. Default-credential   │  green)      │
  │      │ hint removed from the page.                     │              │
  │ D-02 │ ASSET_MODE=local serves pinned, vendored        │ selftest     │
  │      │ copies same-origin. No code path emits an       │ D-02/03      │
  │      │ unpinned third-party script in either mode.     │ (13 files)   │
  │ D-03 │ 'cdn' mode always emits integrity= +            │ asset_script │
  │      │ crossorigin. SRI digests computed from the      │ both modes   │
  │      │ actual served bytes, never fabricated.          │ exercised    │
  │ D-04 │ Vendored fallbacks committed (764 KB). CDN      │ selftest     │
  │      │ outage can no longer blank the admin UI.        │ byte-match   │
  │ FV-04│ header.php <title> now escapes $pageTitle.      │ selftest     │
  │      │ Latent sink closed before it became reachable.  │ FV-04 block  │
  └──────┴────────────────────────────────────────────────┴──────────────┘

  GATE [B] RULING — Lucide ELIMINATED, not pinned.
    355 KB of JavaScript to render 21 icons. Rule B-3 (defence and
    complexity proportionate to blast radius) rejects pinning a dependency
    whose entire utility is 21 static shapes. Replaced by a 4.8 KB
    server-rendered whitelist: a 71x reduction that also removes an origin,
    a script-execution surface, and a runtime bootstrap call.

  GATE [R] RULING — the whitelist is the guard, not the pin.
    icon() resolves names against a fixed table and NEVER interpolates the
    caller's string into output. peers.php passes a computed name; under the
    old markup that was a latent injection sink, under icon() it cannot be.
    Asserted directly: icon('"><script>alert(1)</script>') === ''.

  GATE [M] RULING — ΔF = 0 on every pre-existing task.
    No task gained a step, a decision, a recall item, or a wait. Local
    assets remove a network round-trip per page, so ΔF is weakly negative.

  DEPENDENCY SURFACE
                                      BEFORE   AFTER
    third-party origins (login page)       2       0
    third-party origins (admin panel)      4       0
    floating version specifiers            3       0
    scripts without SRI                    4       0

  SELF-TEST: php admin/selftest.php  →  31/31 invariants hold.

  All residuals from v1.2 are now closed. See §12.2.

### 12.2 SECOND REMEDIATION PASS — FV-05, FV-06, R-01 (v1.3)

```
  ┌──────┬────────────────────────────────────────────────┬──────────────┐
  │ ID   │ REMEDIATION                                     │ VERIFIED BY  │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ FV-05│ session_regenerate_id(true) on successful auth; │ LIVE HTTP:   │
  │  S1  │ CSRF token re-minted with the new session;      │ id rotates   │
  │      │ cookie set httponly + samesite=Lax + secure     │ 0310dcca ->  │
  │      │ when TLS; params set before session_start();    │ dbf91638     │
  │      │ logout clears $_SESSION, cookie AND session.    │              │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ FV-06│ Per-session 256-bit token. csrf_require() on    │ LIVE HTTP:   │
  │  S1  │ every mutating path: users, torrents, login,    │ no token 403 │
  │      │ logout, and the upload API. Accepted from a     │ forged   403 │
  │      │ form field or X-CSRF-Token, so the fetch()      │ valid    302 │
  │      │ upload is covered by the same check.            │              │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ R-01 │ All 14 DB-derived sinks encoded FOR THEIR       │ selftest:    │
  │  S3  │ CONTEXT, across 6 files — not just peers.php:   │ 0 unescaped  │
  │      │ 11 HTML text via e(), 2 URL params via eu(),    │ sinks remain │
  │      │ 1 JS numeric via an (int) cast.                 │              │
  └──────┴────────────────────────────────────────────────┴──────────────┘

  NEW FINDINGS, discovered while implementing and fixed in the same pass:

  ┌──────┬────────────────────────────────────────────────┬──────────────┐
  │ A-01 │ admin/api/parse-torrent.php had NO AUTH AT ALL. │ LIVE HTTP:   │
  │  S1  │ It never loaded config.php, so anyone on the    │ unauth POST  │
  │      │ internet could POST arbitrary bytes into the    │ -> 401       │
  │      │ bencode parser. Now requires auth + token.      │              │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ A-02 │ bdecode() recursed without bound. "llll..." in  │ selftest:    │
  │  S2  │ a few hundred bytes exhausts the stack and      │ depth bound  │
  │      │ kills the process. Bounded at depth 32; string  │ + propagation│
  │      │ lengths range-checked; upload size capped;      │ + range chk  │
  │      │ is_uploaded_file() verified.                    │              │
  ├──────┼────────────────────────────────────────────────┼──────────────┤
  │ A-03 │ formatBytes() was declared in BOTH config.php   │ php -l +     │
  │  S1  │ and parse-torrent.php. Adding the config        │ load test    │
  │      │ require would have been a FATAL redeclaration   │              │
  │      │ — the auth fix would have taken the endpoint    │              │
  │      │ down. Duplicate removed.                        │              │
  └──────┴────────────────────────────────────────────────┴──────────────┘

  A-03 is the argument for the §9.1 tier rule in miniature: the "small
  security fix" would have broken the endpoint outright had it shipped on
  assertion instead of execution.

  NEGATIVE RESULTS, recorded so the finding count is not inflated:
    · torrentInfo.name renders via Alpine x-text (textContent), NOT x-html.
      The parsed-torrent name is attacker-supplied but is not an XSS sink.
    · peers.php $typeIcon is a ternary over two literals, never user input.

  SELF-TEST: php admin/selftest.php  →  61/61 invariants hold.
  END-TO-END: 7/7 live HTTP checks pass against php -S.

  ┌──────┬────────────────────────────────────────────────┬──────────────┐
  │ B-01 │ PRE-EXISTING, NOT INTRODUCED HERE: `go build    │ identical    │
  │  S1  │ ./...` fails. go.mod declares one dependency,   │ failure at   │
  │      │ but tracker/*.go imports eleven that are        │ baseline     │
  │      │ absent: golang-jwt/jwt/v5, lib/pq, prometheus   │ 2b280f0 and  │
  │      │ client_golang, redis/go-redis/v9, otel (+attr,  │ at HEAD; my  │
  │      │ trace), x/crypto/acme/autocert, x/time/rate.    │ commits      │
  │      │ The Go tracker therefore does not compile.      │ touch 0 .go  │
  │      │ NOT FIXED: adding eleven third-party Go deps is │ files        │
  │      │ a principal's decision, not a side effect of an │              │
  │      │ admin-panel security pass — and it is precisely │              │
  │      │ the dependency-drift question of §5 and §11.    │              │
  └──────┴────────────────────────────────────────────────┴──────────────┘

  METHOD DEFECT IN THE v1.2 PASS, recorded against myself:
    v1.2 reported "go build: unaffected". That check was
    `go build ./... 2>&1 | head -3 && echo ok` — the && tests head's exit
    status, which is always 0, so the echo fired unconditionally. The
    claim was true (my commits touch no Go files) but the EVIDENCE for it
    was vacuous. Verified properly here by checking out the baseline and
    diffing the failure output. A green check that cannot go red is not a
    check; rule L5-1 should have caught it and did not.
```

---

## 13. NEXT SAFE STEP

```
  ┌────────────────────────────────────────────────────────────────────┐
  │ LEDGER STATE                                                       │
  │   OBJ-7  amended and ratified      OBJ-8  calibrated (d not derivable)
  │   OBJ-9  ratified                  OBJ-6  mitigated                │
  │   D-01..D-04, FV-04, FV-05, FV-06, R-01, A-01..A-03  ALL CLOSED    │
  │                                                                    │
  │   No S0, S1, or S2 defect is open against the admin panel.         │
  │                                                                    │
  │ REMAINING, BY CLASS                                                │
  │   S3  OBJ-6  persona proliferation — mitigated by rule J-1, not    │
  │              eliminated; revisit if seat commentary degrades       │
  │   S3  OBJ-8  d remains underivable until a repo carries 3+ years   │
  │              of manifest history and 10+ version-change events     │
  │                                                                    │
  │ OPEN, PRE-EXISTING, OUTSIDE THIS PASS                              │
  │   B-01  S1  `go build ./...` fails: 11 imports missing from go.mod.│
  │             Needs a principal ruling on adding those dependencies. │
  │                                                                    │
  │ THE SINGLE NEXT SAFE ACTION                                        │
  │   Rule on B-01. The admin panel is clean; the Go tracker does not  │
  │   compile, which outranks anything else remaining. After that, the │
  │   instrument's Phase II kill criterion (§10)                       │
  │   now has data to judge: this pass found 1 S0 and 4 S1 across a    │
  │   6-file panel — far above the "< 1 per 10 modules" abandonment    │
  │   threshold. The gate array has paid for itself once. Proceed to   │
  │   Phase III (pilot on one NEW module) rather than extending the    │
  │   retrospective sweep.                                             │
  │                                                                    │
  │   Recommended, not required: ADMIN_PASS is currently derived from  │
  │   a hardcoded 'changeme' via password_hash() at request time. That │
  │   is a deployment concern rather than a code defect, but it is the │
  │   weakest remaining link in the panel and should be moved to       │
  │   configuration before any internet-facing deployment.             │
  └────────────────────────────────────────────────────────────────────┘
```

---
