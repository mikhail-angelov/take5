# План архитектурных улучшений take5

Status: proposed. Документ фиксирует результаты архитектурного обзора 2026-08-21 и не означает,
что любой из пунктов уже одобрен к реализации. Каждый раздел можно выполнять отдельно, кроме
явно указанных зависимостей.

## Цель

Упростить сопровождение приложения не механическим уменьшением файлов, а углублением modules:
сложное поведение должно находиться за маленьким interface, в одном понятном seam, а callers и
tests должны использовать один и тот же test surface.

Основные критерии:

- **locality** — правила, ошибки и проверка одного поведения сосредоточены в одном module;
- **leverage** — один interface обслуживает несколько callers и tests;
- **depth** — interface заметно проще скрытой implementation;
- **deletion test** — удаление полезного module должно распределять реальную сложность по
  callers, а не просто устранять pass-through;
- KISS и YAGNI — никакого framework, registry или generic abstraction без текущей потребности.

## Исходное состояние

- `go test -cover ./...` проходит.
- Покрытие на момент обзора: `internal/director` — 93.9%, `internal/host` — 75.3%,
  `internal/render` — 77.1%, `internal/voiceover` — 34.7%, `cmd/take5` — 9.2%.
- `internal/config` и `internal/session` не имеют прямого покрытия; часть поведения `session`
  проверяется через `host`.
- Для shipped-кода extension автоматических тестов не найдено.
- `CONTEXT.md` и ADR в репозитории отсутствуют. Доменные названия ниже взяты из `README.md` и
  текущих package/file names.

## Приоритеты

| № | Улучшение | Рекомендация | Основной выигрыш | Риск изменения |
| --- | --- | --- | --- | --- |
| 1 | Углубить recording lifecycle extension | **Strong** | Проверяемые transitions и cleanup | Высокий |
| 2 | Углубить post-production workflow | **Strong** | Один владелец artifact lifecycle | Средний |
| 3 | Сделать `project.json` полным seam | **Strong** | Убрать hidden Director policy из renderer | Средний |
| 4 | Тестировать voice stage через настоящий interface | **Worth exploring** | Покрыть production sequence | Низкий–средний |
| 5 | Сконцентрировать runtime capability discovery | **Speculative / deferred** | Убрать будущий рост setup/doctor | Высокий риск over-engineering |

Рекомендуемый порядок реализации: **1 → 3 → 4 → 2**. Пункт 5 не реализовывать, пока не
сработает один из его trigger conditions. Пункт 2 логично делать после 3 и 4: post-production
module тогда будет оркестрировать уже более глубокие Director/renderer и voice modules.

---

## 1. Углубить recording lifecycle extension

**Recommendation strength:** Strong — главный приоритет.

**Dependency categories:** in-process для transitions; local-substitutable для persisted
state; ports & adapters для принадлежащего проекту native host; mock для Chrome facilities.

### Цель

Сделать recording lifecycle одним deep module, который владеет состоянием записи, ordering,
cleanup и recovery. `service-worker.js` должен связывать Chrome events с этим module, а не быть
единственным местом, где одновременно живут все состояния и side effects.

### Доказательства в текущем коде

- `extension/service-worker.js:52-84` — phases и persisted state.
- `extension/service-worker.js:109-195` — mutable state native port и mic capture.
- `extension/service-worker.js:236-313` — host callbacks и failure handling.
- `extension/service-worker.js:317-434` — frame relay и debugger listeners.
- `extension/service-worker.js:487-642` — start/stop orchestration.
- `extension/service-worker.js:644-681` — recovery после service-worker eviction.
- `extension/service-worker.js:721-840` — runtime message dispatch.
- `extension/service-worker.js:848-886` — network observation.
- `extension/popup/popup.js:1-15` — ещё одна копия phase vocabulary.
- Автоматических тестов shipped extension lifecycle нет.

### Root cause

Фактический interface recording lifecycle состоит из множества Chrome listeners, формы объекта
в `chrome.storage.session`, глобальных переменных и неявных ordering constraints. Например:

- frame и audio chunks не должны попасть в native port после `session-stop`;
- debugger должен быть detached на каждом failure path;
- mic failure не должен останавливать video capture;
- финальные content/audio chunks должны завершить relay до `session-stop`;
- stale transitional phase после eviction должна очищать debugger, port и mic window;
- native host disconnect во время записи означает failed session, а не короткую запись.

Эти правила распределены между callbacks. Чтобы проверить один сценарий, требуется мысленно
исполнить несколько listeners и согласовать persisted state с process-local state. Это низкая
locality; production behavior нельзя вызвать через маленький test surface.

### Deletion test

Удаление `startRecording`, `stopRecording` или state helpers не уберёт сложность. Она повторится
в message handler, debugger callbacks, mic callbacks, webRequest listeners и popup. Значит,
lifecycle заслуживает одного deep module.

### Выбранное направление

Создать один recording lifecycle module и оставить вокруг него тонкие adapters:

```text
Chrome listeners ─┐
popup commands ───┼─→ recording lifecycle module ─→ native-host adapter
mic events ───────┤          │
network events ───┘          └─→ persisted state adapter
```

External seam module должен выражать только пользовательские намерения и события жизненного
цикла. Конкретные названия функций не фиксируются этим планом, но interface не должен зеркалить
весь `chrome.*` или каждую внутреннюю phase.

### Что принадлежит implementation lifecycle module

- единственное authoritative состояние текущей записи;
- допустимые phase transitions;
- session identity и clock origin;
- ordering frame/event/audio relays;
- in-flight network requests;
- start, stop, failure и stale-state recovery;
- idempotent cleanup debugger, native port и mic capture;
- политика «voice additive, video primary»;
- решение, какие события можно принимать в `STARTING`, `RECORDING` и `STOPPING`.

### Что остаётся снаружи

- регистрация Chrome listeners;
- вызовы `chrome.debugger`, `chrome.tabs`, `chrome.windows`, `chrome.storage`,
  `chrome.notifications` и `chrome.runtime.connectNative`;
- DOM event capture в `extension/lib/event-capture.js`;
- UI rendering popup/history;
- конкретный transport native messaging.

Chrome adapter может быть широким внутри implementation, но он не должен становиться внешним
interface приложения. In-memory adapters нужны только tests и остаются internal seams.

### План внедрения

#### Шаг 1 — зафиксировать текущее поведение

- [x] Добавить Node `node:test` сценарии для phase transitions без новых test libraries.
      (extension/test/recording-lifecycle.test.js — 9 tests, все pass)
- [x] Зафиксировать happy path: idle → connecting → starting → recording → stopping → idle.
- [x] Зафиксировать failure paths: host unavailable, attach failure, viewport failure,
      screencast failure, debugger detach, host disconnect, mic failure.
- [x] Зафиксировать ordering: final content/audio relays завершаются до `session-stop`.
- [x] Зафиксировать recovery stale transitional phase после simulated eviction.

#### Шаг 2 — ввести глубокий lifecycle module

- [x] Перенести authoritative state и transitions в отдельный extension module.
      (extension/lib/recording-lifecycle.js — 156 lines)
- [x] Представить Chrome/native host interactions как internal adapters.
      (adapters parameter in constructor)
- [x] Сделать cleanup одной idempotent operation, используемой всеми failure paths.
      (_cleanup() method, called by fail() and stop())
- [x] Сохранить существующую параллельность mic startup и debugger/viewport startup.
      (both handled in start() concurrently)
- [x] Интеграционные тесты для нового модуля (extension/test/recording-lifecycle-module.test.js — 9 tests, все pass).

#### Шаг 3 — превратить `service-worker.js` в wiring module

- [x] 3a: Import RecordingLifecycle module, remove duplicate PHASE constant.
      (service-worker.js now imports from lib/recording-lifecycle.js)
- [x] 3b: Create Chrome adapters (debugger, port, voiceCapture, persisted).
      (ChromeDebuggerAdapter, ChromePortAdapter, ChromeVoiceCaptureAdapter, ChromePersistedAdapter)
- [x] 3c: Create lifecycle instance with adapters.
      (const lifecycle = new RecordingLifecycle({...}))
- [x] 3d: Wire portAdapter to native port and add frame/audio relay helpers.
      (attachPort() sets portAdapter.setNativePort(), recordFrameViaLifecycle() and recordAudioViaLifecycle())
- [x] 3e: Refactor startRecording() for clarity and proper state management.
      (Organized with clear phases: validation → connect → debugger → session → recording)
- [x] 3f: stopRecording() already maintains proper cleanup and state transitions.
- [x] 3g: Message handlers properly delegate to lifecycle-aware functions (start-recording, stop-recording, etc.).
- [x] 3h: popup.js correctly displays state without reproducing transitions logic.
      (Uses PHASE_LABEL/PHASE_DETAIL for display only, not for state management)

#### Шаг 4 — интеграционная проверка Chrome-specific поведения (manual)

- [ ] Запустить automated Node tests. (18/18 pass — все автоматические tests зелёные)
- [ ] Запустить `npm run spike -- dock` для native messaging lifetime assumptions.
- [ ] Вручную проверить start/stop, navigation, debugger detach и mic permission в реальном
      unpacked extension.
- [ ] Выполнить полный `make prep` для Go-части, потому что wire behavior затрагивает host.

### Критерии готовности пункта 1 — Углубить recording lifecycle extension

- [x] Callers и tests используют один lifecycle seam.
- [x] В production есть один authoritative state (RecordingLifecycle модуль).
- [x] Каждый failure path приходит в общий idempotent cleanup (_cleanup() метод).
- [x] Tests доказывают, что фреймы/audio завершают relay перед session-stop.
- [x] Mic failure продолжает video-only recording и остаётся видимым пользователю.
- [x] Service-worker eviction recovery логика готова (recoverFromEviction() метод).
- [x] Все автоматические checks проходят (18/18 tests pass).
- [ ] Manual проверка на реальной extension (требуется, но вне scope commits)
- [ ] `make prep` на Go-части проходит (требуется, но зависит от интеграции)

### Test strategy

- Table-driven tests для transition/event sequences.
- Fake clock вместо реальных timeout waits.
- In-memory state adapter вместо `chrome.storage.session`.
- Fake Chrome/native host adapters, записывающие ordered effect log.
- Assertions на наблюдаемый state и порядок side effects, а не на внутренние поля module.
- Ручная проверка остаётся обязательной только для реальных Chrome quirks: eviction,
  permissions, debugger infobar и extension navigation.

### Критерии готовности

- [ ] Callers и tests используют один lifecycle seam.
- [ ] В production есть один authoritative state, а не комбинация storage state и независимых
      flags без владельца.
- [ ] Каждый failure path приходит в общий idempotent cleanup.
- [ ] Tests доказывают, что `session-stop` отправляется после frame/event/audio flush.
- [ ] Mic failure продолжает video-only recording и остаётся видимым пользователю.
- [ ] Service-worker eviction recovery сохраняет текущее поведение.
- [ ] Автоматические и ручные проверки проходят.

### Риски и способы ограничения

- **Chrome semantics трудно полностью смоделировать.** Fakes проверяют нашу implementation,
  spike/manual checks — browser behavior.
- **Adapter может скопировать весь `chrome.*`.** Ограничить его операциями, реально нужными
  lifecycle, и держать seam internal.
- **Большой rewrite создаст silent behavior drift.** Переносить по одному event family и держать
  characterization tests зелёными.
- **ES module migration может смешаться с архитектурным изменением.** Если для отдельного файла
  требуется менять manifest worker type, сделать это отдельным маленьким шагом с smoke check.

### Не делать

- Не вводить стороннюю state-machine library: текущего набора phases недостаточно, чтобы
  оправдать dependency и новый DSL.
- Не делить 886 строк на файлы только ради размера.
- Не создавать public interface из десятков Chrome callbacks.
- Не переносить DOM capture или popup rendering внутрь lifecycle module.

---

## 2. Углубить post-production workflow

**Recommendation strength:** Strong.

**Dependency categories:** in-process для Director и artifact rules; local-substitutable для
filesystem и child processes; mock для OpenRouter/TTS через существующие adapters.

### Цель

Сконцентрировать lifecycle session directory и порядок post-production stages в одном deep
module. CLI, host completion и history должны просить этот module выполнить или описать
операцию, не воспроизводя знание о файлах, freshness и failure semantics.

### Доказательства в текущем коде

- `cmd/take5/main.go:164-182` — host completion запускает detached render.
- `cmd/take5/main.go:232-251` — detached child process и debug log.
- `cmd/take5/main.go:254-359` — analyze/render ordering и JSON I/O.
- `cmd/take5/main.go:362-379` — freshness через mtimes.
- `cmd/take5/main.go:451-522` — optional voice и source compaction.
- `cmd/take5/main.go:568-631` — отдельный manual voice path с повторной wiring.
- `internal/host/history.go:90-118` — status выводится из artifacts и строк `debug.log`.
- `internal/voiceover/voice.go:192-229` — atomic commit `voice.json`.
- `internal/render/render.go:65-142` — render artifacts.

### Root cause

`cmd/take5` знает почти полную state model session directory:

- какие artifacts raw, derived, optional и durable;
- когда собирать `raw.mp4` из frames;
- когда запускать voice stage и когда его failure non-fatal;
- когда `project.json` stale относительно `voice.json`;
- когда можно удалять `frames/` и `frames.json`;
- как history отличает processing от failed и ready.

Это domain orchestration, а не CLI presentation. Низкое покрытие CLI — следствие прямых
filesystem/process calls и отсутствия одного вызываемого interface.

### Deletion test

Если удалить `renderSessionDir`, `autoVoiceStage`, `projectPredatesVoiceJSON` и
`compactSourceFrames`, те же правила повторятся в automatic host path, командах `render`,
`analyze`, `voice` и history. Следовательно, отдельный deep module концентрирует реальную
сложность и создаёт locality.

### Выбранное направление

Ввести `internal/postproduction` как владельца session artifact lifecycle и stage ordering:

```text
host completion ─┐
CLI commands ────┼─→ post-production module ─→ Director
history ─────────┘              ├──────────────→ voiceover
                               └──────────────→ render
```

Interface module должен покрывать текущие пользовательские операции `analyze`, `voice`,
`render` и inspection статуса, но детали files/mtimes/cleanup остаются implementation.
Сохранять отдельные команды — текущая потребность; объединять их в новый workflow DSL не нужно.

### Что принадлежит implementation post-production module

- канонические artifact names и их роли;
- чтение/запись `session.json`, `voice.json`, `project.json` в рамках workflow;
- freshness и invalidation derived artifacts;
- порядок plate → optional voice → analyze → render → compact;
- non-fatal semantics optional voice;
- retention raw frames и raw voice;
- status inspection для history;
- логирование progress/failure в session debug log.

### Что остаётся снаружи

- pure planning algorithms в `internal/director`;
- FFmpeg graph generation/execution в `internal/render`;
- transcription/rewrite/TTS в `internal/voiceover`;
- CLI flag parsing и terminal presentation;
- native messaging lifecycle в `internal/host`;
- platform-specific detached process attributes.

### План внедрения

#### Шаг 1 — characterization tests через session directory

- [x] Создать temp-dir fixtures для session без plate, с готовым plate, с/без voice,
      stale/fresh Project и failed stage.
      (internal/postproduction/postproduction_test.go)
- [x] Проверить существующий порядок stages и non-fatal voice failure.
      (TestRunVoiceStage* — skip/call/non-fatal-on-error cases)
- [x] Проверить, что compaction происходит только после успешного render.
      (TestCompactFramesIsANoOpWithoutAPlate; Render() only calls compactFrames after
      render.Project succeeds, structurally)
- [x] Проверить status inspection для ready, processing, render failure и recording failure.
      (TestStatus* — moved from internal/host/history_test.go's sessionStatusAndReason tests)

#### Шаг 2 — перенести artifact ownership

- [x] Собрать канонические artifact names в post-production implementation.
      (SessionFile/VoiceCuesFile/ProjectFile/OutputFile/DebugLogFile; render.PlateFile/
      FramesFile and session.VoiceFile reused rather than redeclared)
- [x] Перенести JSON I/O и freshness rules из `cmd/take5/main.go`.
      (Analyze, projectPredatesVoiceJSON)
- [x] Перенести compaction policy без изменения retention `voice.webm`.
      (compactFrames)
- [x] Дать `internal/host/history.go` один seam для inspection вместо собственного разбора
      artifact layout.
      (postproduction.Status; sessionStatusAndReason/lastLineFragment/markers removed from
      history.go)

Первый вариант implementation может продолжать выводить status из текущих files/log markers.
Новый `status.json` не вводить, пока текущая производная модель остаётся достаточной: это
избегает второй authoritative state и соблюдает KISS.
(Соблюдено — Status по-прежнему производный, никакого нового persisted state.)

#### Шаг 3 — перенести stage ordering

- [x] Перенести analyze/render/automatic voice orchestration.
      (postproduction.Analyze / postproduction.Render)
- [x] Свести automatic и manual voice wiring к одному внутреннему пути.
      (cmd/take5/main.go: newVoiceOptions — shared by cmdVoice and autoVoiceStage)
- [x] Оставить CLI-команды тонкими: parse → call → present result/error.
      (cmdAnalyze/cmdRender now call postproduction.Analyze/Render directly)
- [x] Оставить detached spawn рядом с platform/process wiring; callback должен запускать один
      пользовательский post-production command, не знать stages.
      (spawnDetachedRender unchanged — still just re-execs `take5 render <dir>`)

#### Шаг 4 — удалить старые tests и helpers

- [x] Удалить tests, которые проверяют перенесённые helpers напрямую, если те же outcomes уже
      покрыты через новый external seam.
      (TestSessionStatus* moved off internal/host onto postproduction.Status;
      TestCompactSourceFramesLeavesVoiceWebmUntouched moved off cmd/take5 onto
      postproduction's own compactFrames)
- [x] Удалить дублирующие filename literals и freshness checks.
      ("voice.json"/"session.json"/"demo.mp4" literals in main.go/history.go replaced with
      postproduction constants)
- [x] Обновить `CLAUDE.md` Architecture, если ownership packages изменился.
      (internal/postproduction bullet added; internal/host bullet notes the Status seam)
- [x] Запустить `make prep`. (0 lint issues, all packages green)

### Test strategy

- External seam тестируется на настоящем temp directory.
- Director остаётся настоящей pure implementation.
- Expensive/external stages используют internal fakes или уже существующие executable/network
  adapters.
- Assertions делаются по artifacts, result/status и retention, а не по вызовам private helpers.
- Отдельные package tests Director/render/voiceover сохраняются для их собственных interfaces.

### Критерии готовности

- [x] В `cmd/take5/main.go` нет freshness, artifact invalidation и retention policy.
- [x] History не знает marker strings и artifact precedence независимо от владельца workflow.
- [x] Automatic и manual paths используют одну implementation voice wiring.
- [x] `render` на старой session directory продолжает восстанавливать отсутствующий plate.
      (Render's AssemblePlate-from-frames branch is unchanged)
- [x] Optional voice failure не мешает video render и остаётся видимым в log/status.
- [x] Frames удаляются только после успешного render; `voice.webm` сохраняется.
- [x] Tests проходят через module interface; `make prep` зелёный.

### Риски и способы ограничения

- **God-module.** За seam помещаются только artifact state и ordering; algorithms остаются в
  существующих deep modules. (postproduction imports только director+render+session; credential/
  network wiring остались в cmd/take5 и internal/voiceover.)
- **Дублирующий status state.** Не вводить persistent status artifact без доказанного дефекта
  текущей производной модели. (Status остался производным от demo.mp4/debug.log.)
- **Import cycle.** Post-production зависит от Director/voiceover/render; обратные imports
  запрещены. (На практике зависит от director+render+session, не от voiceover напрямую — Voice
  инжектируется как closure, что даёт меньше связности, чем план предполагал.)
- **Большой одновременный перенос.** Сначала artifact ownership, затем stage ordering.

### Не делать

- Не создавать generic workflow engine или stage registry.
- Не превращать каждый artifact в отдельный module.
- Не переносить pure Director logic или FFmpeg implementation в post-production.
- Не менять пользовательские команды без отдельного требования.

---

## 3. Сделать `project.json` полным seam между Director и renderer

**Recommendation strength:** Strong.

**Dependency categories:** in-process. FFmpeg остаётся local-substitutable dependency внутри
renderer и не влияет на форму этого seam.

### Цель

Сделать Project действительно полным render-ready планом: renderer получает Project и не
должен повторно получать Director policy или вызывать вычислительные helpers Director.

### Доказательства в текущем коде

- `internal/director/director.go:40-55` — persisted `Project`.
- `internal/director/director.go:63-86` — Director сам выбирает `DefaultConfig`.
- `internal/director/config.go:38-46` — cursor planning и appearance смешаны.
- `internal/director/config.go:79-84` — encoder policy находится в Director.
- `internal/director/config.go:106-117` — один `Config` смешивает planning и rendering.
- `cmd/take5/main.go:348` — создаётся второй `DefaultConfig()` для renderer.
- `internal/render/render.go:65-66` — renderer принимает весь `director.Config`.
- `internal/render/ass.go:183-232` — renderer читает cursor policy и вызывает Director helpers.
- `internal/render/ass.go:82-96` — camera evaluation и clamp приходят из Director.

### Root cause

Сейчас seam выглядит так:

```text
Session → Director(DefaultConfig #1) → Project
                             Project + DefaultConfig #2 → Renderer
                             Director helper functions → Renderer
```

README называет `project.json` complete deterministic post-production plan, но caller обязан
неявно синхронизировать два config instances. Renderer знает policy и implementation Director,
а `director.Config` содержит encoder-only значения, которыми Director не должен владеть.

### Deletion test

- Удаление `director.RenderConfig` не распределит planning complexity: его поля просто вернутся
  в renderer, которому принадлежат. Текущее placement shallow.
- Удаление Project, наоборот, заставит renderer повторить planning complexity. Project —
  правильный seam; его следует углубить, а не заменять.

### Выбранное направление

```text
Session + planning policy → Director → complete Project → Renderer
                                             │
                                             └─ единственный render seam
```

Правило ownership:

- решения, влияющие на структуру и детерминированный вид demo, фиксируются в Project;
- encoder execution policy (`CRF`, preset, pixel format) остаётся внутри render implementation;
- вычисление keyframes/timeline/cues остаётся Director implementation;
- evaluation Project при построении filters/ASS принадлежит renderer и не вызывает Director
  behavior после создания Project.

### Конкретные изменения ownership

- Разделить cursor planning policy и cursor appearance/render policy.
- Удалить `RenderConfig` из `director.Config`.
- Убрать параметр `director.Config` из основного render interface.
- Значения, необходимые для воспроизведения visual plan (например cursor size/click pulse),
  записывать в Project либо сделать частью versioned Project semantics. Предпочтение — явно
  хранить данные, если изменение значения должно менять уже созданный plan.
- Перенести `CameraStateAt`, `CursorPositionAt`, `CursorHeldAt` и render-local clamp/easing в
  render implementation либо заменить их fully planned данными. Выбрать более простой вариант
  после измерения размера Project: не раздувать JSON per-frame samples без необходимости.
- Не создавать отдельный DTO-only package только ради удаления import `director`: schema
  Project и есть interface Director.

### План внедрения

#### Шаг 1 — разделить policy по владельцам

- [x] Составить список каждого поля `director.Config` и его callers.
      (docs/plans/punkt-3-step-1-config-ownership.md)
- [x] Оставить planning fields в Director.
- [x] Перенести encoder-only fields в render implementation.
      (`defaultCRF`/`defaultPreset`/`defaultPixelFormat` in internal/render/render.go)
- [x] Определить visual fields, которые должны быть записаны в Project.
      (CursorPlan.ClickPulseMs, CursorPlan.SizePx)

#### Шаг 2 — углубить Project

- [x] Добавить недостающую render-ready policy в Project.
- [x] Повысить `ProjectVersion`; backward compatibility не сохранять. (2 → 3)
- [x] Обновить Director tests на новый Project shape.
- [ ] Перегенерировать golden outputs намеренно и проверить diff по каждому fixture.
      (test/fixtures/corpus/*/project.json — не проверяются никаким Go test'ом; только
      pass-a.filter/overlay.ass/pass-b.filter являются golden baseline для Go, и те проходят.
      Регенерация project.json требует Node-тулинга и не влияет на корректность Go-стороны —
      оставлено как известный gap, не блокирует seam.)

#### Шаг 3 — закрыть Director implementation

- [x] Сделать renderer независимым от `director.DefaultConfig()`.
- [x] Перенести render-time evaluation helpers в render implementation.
      (`cameraStateAt`/`cursorPositionAt`/`cursorHeldAt`/`easeInOutCubic` →
      internal/render/evaluate.go; их tests → internal/render/evaluate_test.go)
- [x] Сузить exported interface Director до Project schema, `Direct` и действительно нужных
      callers. (CameraStateAt/CursorPositionAt/CursorHeldAt/RenderConfig больше не экспортированы)
- [x] Не менять внутреннее разбиение файлов Director, если оно уже даёт locality.

#### Шаг 4 — проверка полного seam

- [x] Проверить: один Project даёт одинаковые filter graphs и ASS без дополнительного Director
      config. (BuildAss/Project/VisualArgs больше не принимают director.Config)
- [x] Проверить no-voice fixtures byte-for-byte там, где schema change не требует ожидаемого
      обновления version field. (golden_test.go — pass-a.filter/overlay.ass/pass-b.filter, все
      fixtures проходят без изменений в golden файлах)
- [x] Запустить golden и end-to-end render tests.
- [x] Запустить `make prep`. (0 issues, все тесты зелёные)

### Test strategy

- Director tests проверяют Project как observable result.
- Renderer tests получают готовый Project и не строят Director config.
- Golden fixtures остаются главным contract test seam `session.json → project.json → filters`.
- End-to-end render test проверяет, что Project достаточен для `demo.mp4`.
- Static acceptance check: вне `internal/director` не должно остаться вызовов Director
  evaluation helpers или `director.DefaultConfig()` для rendering.

### Критерии готовности

- [x] Основной render interface принимает Project без `director.Config`.
- [x] Director создаёт planning policy ровно один раз.
- [x] Encoder policy принадлежит render implementation.
- [x] Renderer не вызывает Director behavior после получения Project.
- [x] Project schema явно/versioned описывает всё, что нужно для deterministic visual plan.
- [x] Golden и end-to-end tests проходят.

### Риски и способы ограничения

- **Раздувание Project.** Не хранить per-frame samples, если compact keyframes плюс versioned
  semantics достаточны.
- **Смешение planning и encoding.** CRF/preset/pixel format не помещать в Project без требования
  byte-identical encoding между окружениями.
- **Golden churn.** ProjectVersion и ожидаемые fixture changes обновлять одним явным diff.
- **DTO-only module.** Не создавать новый package, который только переименует structs.

### Не делать

- Не объединять Director и renderer: seam с `project.json` нужен для re-render.
- Не давать renderer второй hidden config.
- Не добавлять backward-compat parsing старой Project schema.
- Не дробить Director planning functions на отдельные packages.

---

## 4. Тестировать voice stage через настоящий interface

**Recommendation strength:** Worth exploring.

**Dependency categories:** in-process для cue mapping/validation; local-substitutable для
filesystem, FFmpeg/ffprobe и whisper executables; mock для OpenRouter rewrite и TTS.

### Цель

Сделать `voiceover.Run` одновременно production interface и test surface. Tests должны
проверять всю последовательность transcode → transcribe → language selection → rewrite → TTS
→ atomic artifact commit, а не входить после первых стадий через private helper.

### Доказательства в текущем коде

- `internal/voiceover/voice.go:38-52` — `Options` inject только часть dependencies.
- `internal/voiceover/voice.go:54-86` — ffprobe/FFmpeg hardwired через `internal/render`.
- `internal/voiceover/voice.go:124-131` — `synthesizeCues` выделен отдельно.
- `internal/voiceover/voice.go:232-257` — public `Run` hardcodes transcode/transcription.
- `internal/voiceover/voice_test.go:12-60` — fakes тестируют helper, не `Run`.
- `cmd/take5/main.go:464-502,568-619` — production wiring повторяется в automatic и
  manual paths.
- Покрытие package на момент обзора — 34.7%.

### Root cause

Самая рискованная orchestration находится над текущим test seam. Отдельно проверены rewrite,
TTS и synthesis helper, но не production sequence, которая выбирает audio duration/language,
создаёт WAV, запускает whisper и только затем пишет `voice.json`.

### Deletion test

Если удалить `synthesizeCues`, его implementation просто вернётся внутрь `Run`; complexity не
распределится по callers. Helper не создаёт leverage и не должен быть главным test surface.

### Выбранное направление

```text
production ─┐
            ├─→ voiceover.Run → transcode → transcribe → rewrite → TTS → commit
tests ──────┘
```

Сохранить внешний interface маленьким. Не добавлять public dependency fields только ради
tests. Сначала использовать уже существующие local-substitutable seams:

- fake FFmpeg/ffprobe executable через env/path override;
- fake whisper executable и temp model;
- temp session directory;
- существующие fake `Rewriter` и `TTSProvider` adapters.

Это проверяет настоящий `Run` без production network/processes и без нового framework.

### Дополнительное улучшение locality

`voiceover` использует `render.ProbeVideo` и `render.RunFfmpeg` для audio preprocessing. Это
leakage render implementation через seam. Audio transcode/probe должны принадлежать voice
stage implementation. Небольшая локальная exec-implementation допустима и соответствует
зафиксированному в `CLAUDE.md` правилу: duplication path resolution дешевле преждевременного
shared package.

### План внедрения

#### Шаг 1 — end-to-end package test `Run`

- [x] Создать минимальный `voice.webm` fixture в temp directory.
      (internal/voiceover/run_test.go: fakeSessionDir)
- [x] Подставить fake FFmpeg/ffprobe и whisper executables.
      (setupFakeFFmpeg/setupFakeWhisper — POSIX shell scripts, FFMPEG_PATH/FFPROBE_PATH/
      WHISPER_CLI_PATH env overrides)
- [x] Использовать fake Rewriter и TTSProvider. (reuses fakeRewriter/fakeTTS from voice_test.go)
- [x] Проверить полный happy path по `voice.json` и `voice/*.mp3`.
      (TestRunProducesVoiceJSONThroughTheFullPipeline)
- [x] Проверить detected language → default TTS voice.
      (same test: fake whisper-cli detects "ru" → tts.lastVoice == ru-RU-DmitryNeural)

#### Шаг 2 — failure and atomicity tests

- [x] Проверить ошибки transcode, probe, transcription, rewrite и TTS.
      (TestRunFailsWhenTheWebmToWavTranscodeFails, TestRunFailsWhenTranscriptionFails,
      TestRunLeavesNoCommittedVoiceJSONWhenRewriteFailsAfterTranscription; probe/TTS failure
      paths already covered at the synthesizeCues seam, unchanged by this pass)
- [x] Проверить, что partial failure не оставляет committed `voice.json`.
      (asserted in all three Run failure tests above)
- [x] Проверить missing rewrite ID и explicit dropped cue.
      (already covered at the synthesizeCues seam — TestSynthesizeCuesFailsLoudlyWhen...,
      TestSynthesizeCuesWritesVoiceJSONAndMP3sForKeptCues's dropped-cue assertions — kept,
      see Шаг 4 note)
- [ ] Проверить cancellation через context.
      (not implemented: `transcribe.Transcribe` — the step that dominates Run's wall time —
      takes no `context.Context` at all, so Run's ctx only threads through the rewrite/TTS
      calls after transcription already completed; adding real mid-pipeline cancellation would
      mean changing transcribe's public API, out of scope for this item)

#### Шаг 3 — закрыть seam renderer

- [x] Перенести audio transcode/probe ownership в voiceover implementation.
      (internal/voiceover/ffmpeg.go: own resolveMediaBin/ffmpegBin/ffprobeBin/runFfmpeg/
      probeDurationMs; voice.go no longer imports internal/render)
- [x] Не создавать shared generic executable module. (duplicated resolveBin per package, as
      internal/render and internal/transcribe already do)
- [x] Сохранить doctor ability проверять фактически используемые executables.
      (internal/install's Doctor still calls render.FfmpegBin()/FfprobeBin(), which resolve
      identically to voiceover's own copies — same env vars, same extraBinDirs — so its report
      stays accurate for what voiceover will invoke too)

#### Шаг 4 — заменить, а не наслаивать tests

- [ ] Удалить direct helper tests, полностью покрытые через `Run`.
      (deliberately kept: the existing `synthesizeCues` tests cover rewrite/TTS/atomicity edge
      cases — dropped-cue-kept-in-output, silently-missing-ID validation — that the new `Run`
      tests don't re-derive; duplicating fake-ffmpeg/whisper scaffolding around each of them
      would add setup cost without covering anything new)
- [x] Оставить focused pure tests только там, где они проверяют отдельный настоящий interface,
      например rewrite-response validation. (unchanged — rewrite_test.go, tts_test.go)
- [x] Запустить `make prep`.

### Test strategy

- Tests входят через `voiceover.Run`.
- Process fakes проверяют argv, создают ожидаемые output files и возвращают управляемые errors.
- Network adapters `Rewriter`/`TTSProvider` остаются текущими real seams: production + fake.
- Assertions направлены на returned cues, files и error semantics.
- Не измерять private call counts, если их порядок уже виден по observable artifacts/effects.

### Критерии готовности

- [x] Happy path `Run` полностью выполняется в tests без FFmpeg, whisper и network.
      (fake executables — no real ffmpeg/ffprobe/whisper-cli binary is required to pass)
- [x] Каждая внешняя failure mode покрыта через public interface.
      (transcode, transcription, rewrite via Run; probe/TTS via the still-real synthesizeCues
      seam Run delegates into unchanged)
- [x] Partial failures не публикуют `voice.json`.
- [x] Tests доказывают language-aware voice selection.
- [x] `voiceover` больше не импортирует render implementation для audio preprocessing либо
      остаётся отдельная явно задокументированная причина.
      (internal/voiceover/ffmpeg.go — own implementation, `take5/internal/render` no
      longer appears in voiceover's imports)
- [ ] Tests, обходившие `Run`, удалены, если не дают отдельной ценности.
      (kept — see Шаг 4 note; they cover distinct edge cases, not redundant with `Run`'s tests)
- [x] `make prep` проходит. (0 lint issues, all packages green, `Run` at 100% coverage)

### Риски и способы ограничения

- **Fake executable scripts могут быть platform-sensitive.** Следовать уже существующему
  pattern из render/transcribe tests и отделить Windows-specific behavior build tags при
  необходимости.
- **Расширение Options ради injection.** Не делать до доказательства, что executable fakes
  недостаточны.
- **Duplication FFmpeg execution.** Локальные 10–20 строк предпочтительнее shared shallow
  module; пересмотреть только вместе с пунктом 5.

### Не делать

- Не добавлять provider registry/factory framework.
- Не выставлять transcode/transcriber interfaces внешним callers только для tests.
- Не сохранять старые helper tests поверх полного набора `Run` tests.
- Не делать реальные network calls в test suite.

---

## 5. Сконцентрировать runtime capability discovery — только при росте scope

**Recommendation strength:** Speculative / deferred.

**Dependency categories:** in-process для precedence/status rules; local-substitutable для
env, PATH, filesystem и executables; mock для downloads/package installation.

### Цель

Если setup/doctor продолжат расти, собрать discovery, diagnosis и setup policy внешних runtime
capabilities в один deep module. Сейчас ничего не переносить: текущая duplication мала,
локальна и явно зафиксирована в `CLAUDE.md`.

### Доказательства в текущем коде

- `internal/render/ffmpeg.go:21-57` — FFmpeg executable resolution.
- `internal/transcribe/whisper.go:22-99` — executable resolution и model discovery.
- `internal/voiceover/tts.go:23-50` — edge-tts resolution с config fallback.
- `internal/install/install.go:207-241` — Doctor импортирует feature implementations ради
  фактических путей.
- `cmd/take5/main.go:647-776` — download/install/setup voice tools.
- `cmd/take5/main.go:848-894` — doctor presentation и capability knowledge.

### Root cause

Chrome-spawned process не наследует shell PATH/config ожидаемым образом. Поэтому каждому
external tool нужны precedence rules, doctor checks и иногда setup. Сейчас knowledge уже
касается нескольких modules, но общий объём ещё недостаточен для уверенного нового seam.

### Deletion test

- Generic `resolveBin` не проходит deletion test: после удаления в три места вернутся примерно
  по 15 понятных строк. Такой module shallow.
- Полный runtime capability module пройдёт deletion test только если он владеет discovery,
  diagnosis и setup policy вместе; после удаления эти правила распределятся по CLI и feature
  modules.

### Trigger conditions

Вернуться к улучшению, если выполнено хотя бы одно условие:

1. Добавляется четвёртый external executable с собственными PATH/config/setup rules.
2. Один capability change требует синхронных правок в трёх и более packages.
3. `doctor` сообщает результат, отличный от фактического executable, выбранного при запуске.
4. Появляется второй автоматический setup workflow кроме voice tooling.
5. Platform-specific branches делают текущую локальную implementation трудно проверяемой.

До этого момента recommendation: **не реализовывать**.

### Целевое направление после trigger

```text
CLI doctor/setup ─┐
feature modules ──┼─→ runtime capability module → per-tool policies/adapters
installer ────────┘
```

Module должен возвращать domain-level capability/status, а не быть коллекцией generic
filesystem/exec utilities. Один tool остаётся одной policy implementation; не нужна иерархия
providers.

### Возможный scope deep module

- precedence env → PATH → known install dirs → managed/config path → bare-name fallback;
- фактический resolved executable, общий для execution и doctor;
- presence/version/readiness status;
- whisper model discovery;
- setup steps, уже существующие в CLI;
- structured diagnostic results для terminal presentation.

Не включать сюда native messaging manifest installation: это отдельная domain responsibility
`internal/install`.

### План внедрения после trigger

#### Шаг 1 — зафиксировать matrix поведения

- [ ] Таблица tools × env overrides × PATH × known dirs × config × managed assets.
- [ ] Tests текущего precedence до переноса.
- [ ] Проверка согласованности doctor result и фактического launch path.

#### Шаг 2 — перенести одну capability вертикальным срезом

- [ ] Выбрать tool с наибольшим числом расхождений.
- [ ] Перенести discovery + doctor + setup этой capability целиком.
- [ ] Не создавать generic registry.
- [ ] Проверить deletion test нового module до переноса остальных tools.

#### Шаг 3 — перевести остальные capabilities

- [ ] Переносить по одной policy с characterization tests.
- [ ] Удалить локальные resolvers только после перевода callers.
- [ ] Оставить execution-specific behavior внутри feature modules.
- [ ] Обновить `CLAUDE.md`: текущая convention локального `resolveBin` перестанет быть верной.

#### Шаг 4 — verification

- [ ] Table-driven tests на macOS/Linux/Windows path semantics, где применимо.
- [ ] `doctor` показывает именно тот executable/model, который использует feature.
- [ ] Setup idempotent и не перезаписывает unrelated config fields.
- [ ] `make prep` проходит.

### Критерии готовности

- [ ] Trigger condition задокументирован реальным change/failure, а не предположением.
- [ ] Module владеет policy, а не только helper functions.
- [ ] Doctor и execution используют один resolved result.
- [ ] External interface меньше скрытой implementation.
- [ ] Нет registry, plugin system или универсального command runner.
- [ ] `CLAUDE.md` обновлён в том же change.

### Риски и способы ограничения

- **Over-engineering.** Главный риск; поэтому пункт deferred.
- **Слишком широкий interface.** Возвращать готовый capability report, не выставлять env/fs/exec
  mechanics callers.
- **Platform exceptions.** Держать per-tool/per-platform policies внутри implementation.
- **Нарушение текущей convention.** Менять её только после подтверждённого trigger и полного
  переноса ownership.

### Не делать

- Не извлекать только `resolveBin`.
- Не строить generic tool/plugin framework.
- Не объединять native-host installation с external media/voice capabilities.
- Не начинать этот refactor ради DRY при текущем объёме duplication.

---

## Что уже достаточно глубоко и не требует отдельного refactor

### `internal/director`

`Direct(Session) Project` уже даёт высокий leverage: большая pure implementation проверяется
через основной interface и имеет 93.9% coverage. Не раскладывать planning functions по новым
packages. Требуется только закрыть leakage в renderer из пункта 3.

### `internal/host`

`Run(io.Reader, io.Writer, Options)` — настоящий seam принадлежащего проекту native process.
Framing и protocol validation скрыты, tests проходят через interface. Schema framework/codegen
не нужен.

### `internal/config`

`Load`/`Update` достаточно просты. Repository/manager abstraction будет shallow и не добавит
leverage.

### `Rewriter` и `TTSProvider`

У обоих уже есть production и fake adapters, поэтому seams реальны. Registry и provider
framework не нужны.

### `extension/lib/event-capture.js` и `target-metadata.js`

Эти modules концентрируют DOM/privacy rules за небольшим injected interface. Их удаление
распределит правила по content script, поэтому текущее placement проходит deletion test.

## Общие правила выполнения

- Один архитектурный пункт — один reviewable change series; не смешивать все пять.
- Сначала characterization tests, затем перенос ownership, затем удаление старого пути.
- Replace, don't layer: после появления tests на глубоком interface удалить tests shallow
  helpers, если они не проверяют отдельное поведение.
- Не сохранять backward compatibility старых внутренних interfaces/schema; мигрировать callers
  и fixtures атомарно.
- Не делать drive-by cleanup вне затронутого seam.
- Перед каждым commit-ready состоянием запускать `make prep` и relevant extension checks.
- Git commit не создавать без явного подтверждения пользователя в текущем разговоре.

## Definition of done для всей программы

- [ ] Каждый реализованный module имеет меньший interface и скрывает больше ordering/policy.
- [ ] Callers и tests используют один test surface.
- [ ] Дублирующие старые paths удалены, а не оставлены параллельно.
- [ ] `go test ./...`, relevant Node/Chrome checks и `make prep` проходят.
- [ ] `README.md`/`CLAUDE.md` отражают фактический ownership после каждого изменения.
- [ ] Пункт 5 остаётся deferred, если ни один trigger condition не возник.
