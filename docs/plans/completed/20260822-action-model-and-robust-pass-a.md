# План — корректная модель действий пользователя и устойчивый Pass A рендера

Status: COMPLETE — all 6 tasks done, `make prep` green.

## Цель

Post-mortem по реальной записи `2026-08-22-113521`: аннотации и озвучка в `demo.mp4`
казались рассинхронизированы с действиями на экране. Расследование в этой же сессии
показало, что `internal/director` считает тайминги абсолютно корректно — рассинхрон
целиком в `internal/render`: `project.Timeline` этой записи содержал 56 сегментов (после
коммита `3301d78` "optimize writter", заменившего `classifyTypingGap`+`KeepGapMs=600` на
`classifyTypingGapWithBeat` с фиксированным beat=150мс на КАЖДУЮ паузу между нажатиями
клавиш — то, что раньше сливалось в один сегмент через `buildTimeline`, разбилось на ~45
отдельных hold/cut сегментов). `BuildTemporalFilterGraph`'s `split=N` + N×`trim` +
`concat=n=N` filter_complex не масштабируется на N=56: воспроизведено напрямую (тот же
filter_complex против того же `raw.mp4`) — FFmpeg молча роняет тысячи декодированных
кадров, Pass A отдаёт видео 22.35с вместо нужных 31.65с, а Pass B (ASS-субтитры + аудио/
voice микс) всё равно рассчитан на полные `project.DurationMs` (31648мс), потому что не
проверяет фактическую длину, которую отдал Pass A. Итог: последние ~30% ролика — звук и
субтитры без видео.

Договорено с пользователем (см. обсуждение в этой сессии) чинить это не точечным патчем, а
двумя независимыми, но связанными исправлениями:

1. **Корректная модель действия пользователя** — действие определяется как press→hold→release
   / start→end, а не как мгновенное DOM-событие. Ключевое следствие: печать (набор текста) —
   это ОДНО действие пользователя (от первого нажатия до последнего в рамках одного поля), а
   не последовательность независимых мгновенных событий. Это одновременно устраняет корневую
   причину взрыва числа сегментов (у этой конкретной записи) И приводит модель данных в
   соответствие с тем, как пользователь на самом деле думает о своих действиях.
2. **Pass A устойчив к числу сегментов независимо от (1)** — реальные записи всё равно могут
   быть длинными, с большим числом кликов/драгов без всякой печати. `BuildTemporalFilterGraph`
   заменяется на покомпонентное кодирование сегментов + concat demuxer (`-c copy`), по образцу
   уже проверенного `internal/plate`, а не на filter_complex, который принципиально не
   масштабируется.

Пользователь явно попросил: **логика постпроцессинга должна оставаться максимально простой и
прямолинейной, без сложных эвристик.** Везде, где текущая система вводит подстройку/эвристику
(typing beat+hold), это исправление её убирает, а не добавляет новую (typing run → один
сегмент с фиксированной константой скорости, никакого адаптивного расчёта).

## Критерии успеха

- Печать в одно поле — одно действие в терминах `internal/director` (один span, один
  сегмент таймлайна), независимо от количества нажатий клавиш.
- Простой клик — тоже реальный press→release span (`startMs`/`endMs`), а не мгновенная
  точка, как уже сделано для `drag`/`scroll`.
- По-клавишный SFX (`planAudio`), позиционирование курсора (`planCursor`) и слияние
  одинаковых подряд идущих подписей (`planAnnotations`) не теряют по-клавишную
  гранулярность — они продолжают читать исходный список `actions`, а не единственный
  схлопнутый `Action`.
- `TypingConfig.CollapseToMs`, `classifyTypingGapWithBeat`, `isTypingGap`-как-классификатор
  гэпов и `computeTypingOverrides` удалены как более не нужные — не заменены на другую
  эвристику.
- `BuildTemporalFilterGraph` (split+trim+concat filter_complex) удалён. Pass A кодирует
  каждый сегмент таймлайна отдельным, маленьким FFmpeg-вызовом и склеивает результат через
  concat demuxer с `-c:v copy` — число сегментов (10, 56, 200) не влияет на корректность,
  только на время сборки.
- После Pass A физическая длительность собранного клипа проверяется `ffprobe` и должна
  совпадать с `project.DurationMs` в пределах одного кадра; расхождение — явная ошибка
  рендера, а не тихая передача рассинхронизированных видео/аудио в Pass B.
- Повторный прогон реальной записи `2026-08-22-113521` (пересчитанный `project.json` +
  пересобранный `demo.mp4`) даёт видео- и аудио-дорожки одинаковой длины и субтитры,
  совпадающие с происходящим на экране на всём протяжении ролика.
- `make prep` зелёный; golden fixtures (`test/fixtures/corpus/*`) обновлены и отражают новое
  поведение осознанно, не оставлены рассинхронизированными со старым форматом.

## Не входит в scope

- Изменение `internal/plate` (сегментированная сборка `raw.mp4` во время записи) — она уже
  корректна, инцидент был не там.
- Отдельный третий физический FFmpeg-проход для голосовых аннотаций. `planVoicePlacement`
  уже строит фризы для голоса ДО Pass A, как часть единого `project.Timeline` — новый
  устойчивый Pass A (component-encode + concat) обрабатывает эти HoldMs-сегменты тем же
  путём, что и любые другие. Отдельный проход добавил бы сложность (лишний re-encode) без
  выигрыша, который уже не даёт единая модель таймлайна.
- Изменение Pass B (`BuildVisualFilterGraph`, `BuildAss`, `BuildAudioFilterGraph`,
  `internal/render/visual_renderer.go`) — он уже корректно читает `project.DurationMs` и
  весь `project.json`; проблема была только в том, что Pass A не гарантировал совпадение с
  этой длительностью.
- Изменение `internal/session`/`internal/host` (session.json, event capture pipeline вне
  extension) и `internal/postproduction`.
- Адаптивная/эвристическая скорость печати (например, подбор скорости по длине текста или
  по паузам внутри run) — фиксированная константа в `Config`, не алгоритм.
- Retina/touch-специфичные жесты, multi-touch — вне текущей модели событий, не трогаем.

## Архитектура

### Модель действия (extension + director)

Уже корректно реализовано, изменений не требует:
- `drag` (`extension/lib/event-capture.js`): `pointerdown` → `pointermove`×N →
  `pointerup`, уже несёт `startMs`/`endMs`/`path`.
- `scroll`: wheel-gesture с `startMs`/`endMs`, закрывается по `SCROLL_IDLE_MS` простоя.
- Обычное перемещение мыши без нажатой кнопки уже не входит в `meaningfulKinds`
  (`internal/director/pause_classifier.go`) — не действие, как и должно быть.

Требует изменения:
- `click`: сейчас несёт только момент `t` (в районе `pointerup`), без реального
  `mousedown → mouseup` диапазона. `onPointerDown`/`onPointerUp` уже трекают это время для
  drag-детекции — нужно прокинуть тот же интервал в `click`, когда драг не произошёл.
- Печать: `input`-события остаются в session.json как есть (по одному на нажатие — это
  сырые данные, их менять не нужно), но группировка "это один typing run" — которая уже
  существует как `computePairableUnits`/`pairableUnit` в `internal/director/voice.go`,
  сегодня используемая ТОЛЬКО для подбора voice-cue — становится источником сегментов
  таймлайна, а не только пары "voice cue → действие".

### Таймлайн (before/after)

**До:** `planSegments` в `internal/director/pause_classifier.go` идёт по плоскому списку
`actions` (одно нажатие клавиши = один action = потенциальный gap до следующего). Печать из
20 нажатий = 19 внутренних gap-ов, каждый классифицируется отдельно
(`classifyTypingGapWithBeat`), у каждого gap-а по 1–2 сегмента (cut или hold) →
взрывной рост сегментов.

**После:** `planSegments` идёт по `computePairableUnits(actions)` — тот же список, что уже
строит `voice.go`, просто теперь единый источник правды для сегментации, а не только для
voice-pairing. Печать из 20 нажатий — один `pairableUnit`, один сегмент таймлайна с
фиксированной скоростью. Между `pairableUnit`-ами (а не между отдельными нажатиями)
применяется вся существующая логика gap-классификации (`natural`/`hesitation`/
`network-wait`) без изменений — просто на более крупных, семантически осмысленных
интервалах.

### Render Pass A (before/after)

**До:** один FFmpeg-вызов, один `filter_complex`: `split=N` на N веток → N× `trim` →
`concat=n=N`. Не масштабируется на большое N — ffmpeg должен буферизовать декодированный
поток одновременно для всех N веток (`concat` не может забрать ветку 0, пока её `trim` не
получит EOF от `split`, а `split` не даст EOF, пока не кончится источник целиком).

**После**, по образцу уже проверенного `internal/plate` (`internal/plate/segment.go`'s
`encodeBatch`/`buildConcatList`/atomic rename, `internal/plate/finalize.go`'s
`assembleFromSegments`):
```text
project.Timeline (N сегментов, включая HoldMs-фризы от voice-плейсмента)
      |
      v
  для каждого сегмента: отдельный маленький FFmpeg-encode
  (seek+trim+setpts retime ИЛИ seek+один кадр+tpad hold)
      |
      v
  .tmp/pass-a-segments/000000.mp4 ... 000055.mp4
      |
      v
  concat demuxer, -c:v copy, -t durationMs   (как assembleFromSegments для raw.mp4)
      |
      v
  clean.mp4 -> ffprobe -> сравнить с project.DurationMs (±1 кадр) -> ошибка при расхождении
      |
      v
  Pass B (без изменений)
```
Каждый сегмент кодируется независимо — число сегментов больше не влияет на то, будет ли
FFmpeg успешно держать в памяти весь граф разом. Стоимость — N отдельных процессов FFmpeg
вместо одного; для типичной записи (после фикса модели действий — десятки, не сотни
сегментов) это существенно дешевле по риску, чем цена тихого обрезания видео.

## Технические детали

### `Config` (`internal/director/config.go`)

- Удалить `TypingConfig.CollapseToMs` и весь `TypingConfig` в его нынешнем виде (концепция
  "beat" исчезает).
- Добавить взамен (тот же struct name и слот в `Config`, чтобы не трогать остальные поля):
  ```go
  // TypingConfig configures how a typing run — a contiguous same-field run of keystrokes,
  // grouped by computePairableUnits — plays back. It is real footage, not synthetic:
  // Speed simply compresses the dead time between keystrokes uniformly across the whole
  // run, the same way network-wait/hesitation compression already does elsewhere,
  // instead of collapsing each inter-keystroke gap to its own beat/hold.
  type TypingConfig struct {
      Speed float64
  }
  ```
  Default `Speed: 4` как отправная точка — подобрать/подтвердить визуально при рендере
  регрессионной записи в Task 3, это продуктовая, не алгоритмическая настройка.
- Удалить `VoiceConfig.MinKeystrokeGapMs` (использовался только `computeTypingOverrides`,
  которая удаляется целиком — см. Task 3).

### `internal/director/pause_classifier.go`

- `classifyGap` теряет параметры `prev, next *Action` и `typingOverrideMs *float64` —
  типизация становится `classifyGap(startMs, endMs float64, busy []Interval, config Config) *GapDecision`.
  Удалить весь typing-branch в начале функции.
- Удалить `isTypingGap`-использование как классификатора (функция `isTypingGap` остаётся,
  но теперь используется только `computePairableUnits` как предикат группировки — переехавшая
  туда же, см. ниже).
- Удалить `classifyTypingGapWithBeat` целиком.
- `planSegments` меняет сигнатуру: принимает `units []pairableUnit` вместо (или в
  дополнение к, если по-клавишный `Action` нужен для чего-то ещё в этой функции — сверить
  при реализации) плоского перебора `actions`. Дополнительно принимает `Action` для одного
  нового случая: определение спана типингового сегмента как `RawSegment{SourceStartMs:
  unit.StartMs, SourceEndMs: unit.EndMs, Speed: config.Typing.Speed}` для юнитов, чей
  `ActionIndexes` длиннее 1 (typing run), и `Speed: 1` (как сегодня) для остальных.
  `classifyHead`/`classifyTail` тоже переходят на первый/последний `unit` вместо
  `actions[0]`/`actions[len-1]`.
- Убрать параметр `typingOverrides map[int]float64` из `planSegments` — источник этого
  параметра (`computeTypingOverrides`) удаляется в Task 3.

### `internal/director/voice.go`

- `computeTypingOverrides` удаляется целиком (не нужна: печать больше не имеет внутренних
  gap-ов, которые можно было бы растягивать под голос).
- `pairableUnit`/`computePairableUnits` переезжают в `pause_classifier.go` (там их основной
  потребитель теперь), `voice.go` продолжает их использовать как раньше (`pairVoiceCues`,
  `planVoicePlacement`) — просто без владения типом.
- Комментарий в `director.go`'s `Direct()` про "voice pairing меняет сегменты, поэтому
  должна idti перед planSegments" больше не верен — переписать: `computePairableUnits`
  считается один раз, используется и для voice-pairing, и для сегментации; порядка
  зависимостей между ними больше нет.

### `internal/director/director.go`

- `Direct()`: посчитать `units := computePairableUnits(actions)` один раз, передать в
  `planSegments` и в `pairVoiceCues`. Убрать `typingOverrides`/`computeTypingOverrides` из
  пайплайна.

### `extension/lib/event-capture.js`

- Новое closure-состояние `lastPressDown` (аналог `pointerDown`, но не обнуляется
  `onPointerUp`, если жест не стал drag'ом — DOM гарантирует порядок
  `pointerdown → pointerup → click`, так что `onClick` всегда видит актуальное значение,
  выставленное последним `onPointerDown`).
- `onPointerDown`: помимо текущего, `lastPressDown = { t, x: e.clientX, y: e.clientY }`.
- `onPointerUp`: если жест стал `drag` (уже проверенные условия `moved`/`duration`) —
  `lastPressDown = null` (span уже покрыт `drag`-событием, `click` для него дублировать
  не нужно).
- `onClick`: если `lastPressDown` не `null`, добавить `event.startMs = lastPressDown.t`,
  `event.endMs = t`, затем `lastPressDown = null`. Иначе (нет предшествующего
  `pointerdown` — оборонительный случай) — оставить `click` как сегодня, мгновенным.
- **Известный edge case, оставляем осознанно, не чиним отдельным механизмом**:
  `lastPressDown` очищается только когда жест стал `drag` или когда его забрал `onClick`.
  Если `pointerup` происходит, но `click` по каким-то причинам не срабатывает (например,
  элемент под курсором удалён/заменён между `pointerup` и синтетическим `click`), значение
  переживёт этот жест и приклеится к следующему реальному клику как его (неверный)
  `startMs`. Это редкий сценарий с малой ценой ошибки (span клика будет неточным, а не сама
  запись сломается), поэтому в Task 1 не вводим TTL/доп.проверку target — просто
  зафиксировать это в комментарии кода рядом с `lastPressDown`, чтобы не выглядело
  недосмотром при код-ревью.

### `internal/render/temporal_renderer.go`

- Удалить `BuildTemporalFilterGraph`, `segmentFilterChain`, `TemporalArgs`, `Temporal`
  (текущая single-filter-graph реализация).
- Новые пары pure/impure функций, по образцу `internal/plate/segment.go`:
  - `PlanTemporalJobs(timeline []director.TimelineSegment) []TemporalJob` — pure,
    по одному job на сегмент (индекс + сегмент), фильтрует вырожденные (нулевой длины,
    не hold) как сегодня делает валидация в начале `BuildTemporalFilterGraph`.
  - `TemporalSegmentArgs(source string, job TemporalJob, fps int, outPath string) []string`
    — pure, строит ffmpeg argv для ОДНОГО сегмента: обычный сегмент — seek+trim+
    `setpts=(PTS-STARTPTS)/speed`; hold-сегмент — seek на один кадр +
    `tpad=stop_mode=clone:stop_duration=...` (та же арифметика `frameMs`/`padMs`, что
    сегодня в `segmentFilterChain`, просто как самостоятельный вызов, не ветка общего
    графа). Формат чисел (`seconds()`/`formatFixedKeep`) переиспользовать как есть —
    это уже проверенный, golden-протестированный код.
  - `BuildTemporalConcatList(clipPaths []string) string` — pure, `ffconcat version 1.0`
    + `file '...'` построчно (клипы уже несут точную длительность каждый, в отличие от
    `internal/plate`'s JPEG-конкат-листа никакие `duration`-директивы не нужны — прямая
    аналогия с `assembleFromSegments`'s финальным конкат-листом для `raw.mp4`).
  - `Temporal(dir, source string, timeline []director.TimelineSegment, output string, fps int) error`
    — impure orchestration: mkdir временной директории под клипы, прогнать
    `PlanTemporalJobs`+`TemporalSegmentArgs`+`RunFfmpeg` по каждому job
    последовательно (без фоновых воркеров — Pass A уже выполняется в detached
    `render`-процессе, не в live-critical пути), затем concat demuxer (`-c:v copy`,
    `-t durationMs`, `-f mp4`, atomic `.part`+rename — тот же паттерн, что
    `assembleFromSegments`), затем `ProbeVideo` результата и сверка с ожидаемой
    длительностью (см. ниже), затем удаление временной директории с клипами.

### `internal/render/render.go`

- `Project()`: заменить вызов `Temporal(dir, project.Source, passAFilter, cleanFile, fps)`
  (текущая filter-graph сигнатура) на новую `Temporal(dir, project.Source, project.Timeline, cleanFile, fps)`.
  Убрать запись `passAFilter`/`BuildTemporalFilterGraph` целиком — план строится и
  исполняется внутри нового `Temporal`.
- После `video, err := ProbeVideo(filepath.Join(dir, cleanFile))` (уже существующая
  строка) добавить guard:
  ```go
  wantMs := project.DurationMs
  gotMs := int64(0)
  if video.DurationMs != nil { gotMs = *video.DurationMs }
  frameMs := int64(1000 / fps)
  if diff := gotMs - wantMs; diff > frameMs || diff < -frameMs {
      return Result{}, fmt.Errorf("pass A produced %dms of video but project.json expects %dms (off by %dms) — refusing to continue into pass B with mismatched audio/video length", gotMs, wantMs, diff)
  }
  ```
  (Точное место/формулировку уточнить при реализации — важно, что проверка обязана
  случиться ДО построения ASS/audio graph, а не после.)

  **Риск, который нужно закрыть явно, а не "уточнить при реализации":** новый Pass A ищет
  (`seek`) в `raw.mp4` независимо для каждого сегмента (потенциально десятки отдельных
  процессов FFmpeg), а не одним непрерывным decode, как раньше. Если сиковая точность
  окажется хуже кадра (типичный компромисс fast/keyframe-seek `-ss` перед `-i` против
  точного decode-seek `-ss` после `-i`), guard на "±1 кадр" начнёт фейлить рендеры, которые
  раньше молча проходили с приемлемым дребезгом — существующий `render_test.go`'s
  `TestEndToEndRenderProducesAWorkingDemo` уже допускает до 500ms дрейфа на Pass B, то есть
  прецедент терпимости к неточности в этом рендере уже есть, а новый guard по сравнению с
  ним на порядок строже. Task 4 должен явно решить и зафиксировать seek-стратегию (accurate
  `-ss` после `-i`, раз каждый сегмент кодируется отдельным маленьким процессом — цена
  decode-seek здесь не складывается в один длинный процесс, как это было бы при одном общем
  проходе) и проверить её реальным ffprobe на нескольких сегментах разной длины/скорости,
  а не полагаться на то, что "как-нибудь сойдётся".

## План реализации

### Task 1: `click` несёт реальный press→release span — DONE

**Файлы:**
- Modify: `extension/lib/event-capture.js`
- Create: `extension/test/event-capture.test.js`

Работа:
- [x] Добавить `lastPressDown`, выставляемое в `onPointerDown`, читаемое и очищаемое в
      `onClick`, обнуляемое в `onPointerUp` при переходе жеста в `drag`.
- [x] `onClick` прокидывает `startMs`/`endMs` в emitted event, когда `lastPressDown` задан.
- [x] Убедиться, что `drag` по-прежнему не даёт задвоенного `click` со своим собственным
      span (текущее поведение — оба события эмитятся, `click` теперь просто тоже несёт
      span; при реализации свериться, не нужно ли из-за этого поменять что-то в
      `internal/director` — см. Technical Details, ожидание: не нужно, `click`-action при
      наличии одновременного `drag`-action на тех же координатах уже сегодня просто
      два разных `Action` в списке).
- [x] Написать тесты в `extension/test/event-capture.test.js` (по образцу
      `extension/test/recording-lifecycle-module.test.js` — `node:test` + `assert`,
      без сторонних библиотек; минимальный fake `document`/`EventTarget`, как уже
      делает `recording-lifecycle-module.test.js` для `chrome.*`): обычный клик получает
      `startMs < endMs`, совпадающие с моментами `pointerdown`/`pointerup`; клик без
      предшествующего зафиксированного `pointerdown` (оборонительный путь) не ломается и
      не выставляет `startMs`/`endMs`; клик, следующий за жестом, который стал `drag`, не
      наследует его span.
- [x] Прогнать тесты — зелёные перед Task 2.

### Task 2: печать — одно действие на уровне сегментации таймлайна — DONE

**Файлы:**
- Modify: `internal/director/pause_classifier.go`
- Modify: `internal/director/voice.go`
- Modify: `internal/director/director.go`
- Modify: `internal/director/config.go`
- Modify: `internal/director/pause_classifier_test.go`
- Modify: `internal/director/voice_test.go`
- Modify: `internal/director/camera_test.go`
- Modify: `internal/director/annotations_test.go`
- Modify: `internal/director/cursor_test.go`

Работа:
- [x] **Обязательно первым шагом**: `camera_test.go:20`, `annotations_test.go:16`,
      `cursor_test.go:20` каждый определяет свой `planFor*`-хелпер, который вызывает
      `planSegments(actions, durationMs, nil, testConfig, nil)` — текущей 5-аргументной
      сигнатурой (с `typingOverrides` пятым параметром). Без правки этих трёх хелперов под
      новую сигнатуру `go build ./internal/director/...` сломается сразу же — это не
      опциональный шаг, а условие компилируемости всего пакета.
- [x] Перенести `pairableUnit`/`computePairableUnits` из `voice.go` в
      `pause_classifier.go`.
- [x] Заменить `TypingConfig.CollapseToMs` на `TypingConfig.Speed` (default `4`) в
      `config.go`; удалить `VoiceConfig.MinKeystrokeGapMs`.
- [x] Переписать `planSegments` на обход `units []pairableUnit` вместо `actions`:
      typing-юнит (`len(ActionIndexes) > 1`) → один `RawSegment` со `Speed:
      config.Typing.Speed`; прочие юниты — как сегодня (`Speed: 1`, только если
      `EndMs > StartMs`). `classifyHead`/`classifyTail` читают первый/последний unit.
- [x] Упростить `classifyGap`: убрать `prev, next *Action`, `typingOverrideMs *float64`,
      весь typing-branch и вызов `classifyTypingGapWithBeat`.
- [x] Удалить `classifyTypingGapWithBeat` из `pause_classifier.go`.
- [x] Удалить `computeTypingOverrides` из `voice.go`; убрать её вызов и
      `typingOverrides`-проброс из `Direct()` в `director.go`; посчитать
      `units := computePairableUnits(actions)` один раз, передать в `planSegments` и
      `pairVoiceCues`.
- [x] Обновить/переписать комментарий в `director.go`'s `Direct()` про порядок
      voice-pairing/planSegments (зависимость исчезла — обе стороны теперь просто читают
      один и тот же `units`).
- [x] Обновить `pause_classifier_test.go`: заменить/удалить тесты
      `classifyTypingGapWithBeat`/`classifyGap`-с-typing-override на тесты нового
      поведения `planSegments` с `units` — печать даёт один `RawSegment` с
      `Speed == config.Typing.Speed`, независимо от числа нажатий внутри run;
      прогнать существующие table-driven кейсы (natural/hesitation/network-wait) без
      изменений логики, только через новую сигнатуру.
- [x] Обновить `voice_test.go`: удалить тесты `computeTypingOverrides`; проверить, что
      `pairVoiceCues`/`planVoicePlacement` не регрессировали при переезде типа
      `pairableUnit` (чисто механический перенос, тесты должны остаться зелёными без
      изменения ожиданий).
- [x] Прогнать `go test ./internal/director/...` — зелёные перед Task 3.

### Task 3: обновить golden fixtures director-слоя — DONE

**Файлы:**
- Modify: `test/fixtures/corpus/fast-typing/project.json` (и любые другие сценарии с
  печатью — сверить по `tools/gen-fixtures.mjs`, вероятно только `fast-typing` и
  `long-pause-in-field`)
- Modify: `internal/render/golden_test.go` (если формат/список сценариев требует правки)

Работа:
- [x] Прогнать `npm run fixtures:gen` (или эквивалент) / пересчитать golden
      `project.json` для сценариев с печатью через новый `planSegments` — подтвердить,
      что typing run теперь один сегмент с `speed == 4` (или выбранной константой) и что
      результат визуально разумен (не самоцель байт-в-байт, а осмысленная проверка: длина
      typing-сегмента в output time стала короче исходной в ~4 раза, аннотации/voice
      по-прежнему совпадают по времени с исходными нажатиями через
      `sourceTimeToOutputTime`). **Не только `fast-typing`/`long-pause-in-field`**:
      `tools/gen-fixtures.mjs`'s `shortcut-and-mixed` сценарий тоже содержит два
      подряд идущих `input`-события на одном текстовом поле (`t: 2000` и `t: 2300`,
      один и тот же target) — это уже typing run по `isTypingGap`, значит его
      `project.json`/`pass-a.filter` тоже поменяются. Явно свериться по всем сценариям в
      `tools/gen-fixtures.mjs`, а не полагаться на память о том, где "точно" есть печать.
- [x] Обновить golden `project.json` в `test/fixtures/corpus/*` осознанно (не автогенерация
      вслепую — свериться глазами с diff).
- [x] Прогнать `go test ./internal/render/... -run Golden` — зелёные перед Task 4.

### Task 4: Pass A — покомпонентное кодирование + concat demuxer — DONE

**Файлы:**
- Modify: `internal/render/temporal_renderer.go`
- Modify: `internal/render/render.go`
- Modify: `internal/render/temporal_renderer_test.go`
- Modify: `internal/render/golden_test.go`
- Delete: `test/fixtures/corpus/*/pass-a.filter` (заменяются новым golden-артефактом —
  см. ниже)

Работа:
- [x] Реализовать `PlanTemporalJobs`, `TemporalSegmentArgs`, `BuildTemporalConcatList` как
      pure-функции в `temporal_renderer.go` (см. Technical Details) — переиспользовать
      `seconds()`/`formatFixedKeep`/frameMs-hold арифметику из текущего
      `segmentFilterChain` дословно, только не как фрагмент общего filter_complex, а как
      аргументы самостоятельного вызова.
- [x] Реализовать impure `Temporal(dir, source string, timeline []director.TimelineSegment, output string, fps int) error`:
      по образцу `internal/plate/segment.go::encodeBatch` + `internal/plate/finalize.go::assembleFromSegments`
      — encode каждого job в `.tmp/pass-a-segments/NNNNNN.mp4` (атомарно, `.part`+rename,
      как в `internal/plate`), затем concat demuxer `-c:v copy -t durationMs -f mp4` в
      `output` (тоже атомарно), затем удаление `.tmp/pass-a-segments/`.
- [x] Удалить `BuildTemporalFilterGraph`, `TemporalArgs`, старый `Temporal`. **Отклонение от
      плана**: `segmentFilterChain` НЕ удалён — `TemporalSegmentArgs` вызывает его напрямую,
      что и есть буквальное переиспользование его trim/setpts/tpad-арифметики, которое просил
      этот же пункт плана; удаление и обратное воссоздание той же логики под другим именем
      добавило бы дублирование без выигрыша.
- [x] `render.go`'s `Project()`: обновить вызов на новую сигнатуру `Temporal`; убрать
      запись `passAFilter`-файла.
- [x] Добавить post-Pass-A duration guard в `render.go` (см. Technical Details) — сравнение
      `ProbeVideo(cleanFile)` с `project.DurationMs`, ошибка при расхождении больше одного
      кадра.
- [x] Переписать `temporal_renderer_test.go`: тесты на `PlanTemporalJobs` (число job-ов =
      число валидных сегментов, hold и speed-сегменты не выпадают), на
      `TemporalSegmentArgs` (числовые значения seek/trim/tpad совпадают с тем, что раньше
      проверяли golden-тесты на `segmentFilterChain`, включая существующие граничные кейсы
      — см. текущие `{SourceStartMs: 1500, SourceEndMs: 1500, HoldMs: 400}` и подобные),
      на `BuildTemporalConcatList` (формат строк). Реальный FFmpeg-прогон `Temporal()` —
      integration-тест по образцу `internal/plate/recorder_test.go`
      (`TestRecorderEndToEndProducesRawMp4`): синтетический source-клип с известными
      timestamps → собранный `clean.mp4` имеет ожидаемую длительность/число кадров.
      **Специально добавить регрессионный тест на N=56+ сегментов** (в духе повторения
      условий инцидента) — подтверждает, что новая реализация не роняет кадры на большом N.
- [x] Обновить `golden_test.go`: заменить golden-сравнение `BuildTemporalFilterGraph`
      output на golden-сравнение `PlanTemporalJobs`+`TemporalSegmentArgs` per-job
      (сериализовать в детерминированный текст/JSON — тот же дух "чистая функция → золотой
      файл", что сейчас, просто другая форма данных). Удалить старые
      `test/fixtures/corpus/*/pass-a.filter`, добавить их замену (например
      `pass-a-jobs.json` per scenario) — сгенерировать заново, свериться глазами.
- [x] Прогнать `go test ./internal/render/...` — зелёные перед Task 5.

**Дополнительный фикс, найденный при сквозной проверке (Task 5), но относящийся к файлам
этой задачи**: `segmentFilterChain`'s hold-ветка (`tpad=stop_mode=clone:stop_duration=…`)
молча паддила 0 кадров, когда `trim` перед ней отдавал единственный кадр — `tpad` не может
посчитать нужное число кадров по duration без явного frame rate у входного потока, а
однокадровый поток после `trim` не несёт его сам по себе. Не поймано golden-тестами (они
сравнивают только сгенерированную строку filter graph, не реальный прогон FFmpeg) —
поймано именно новым post-Pass-A duration guard на реальной записи. Пофикшено вставкой
`fps=%d` перед `tpad` в `segmentFilterChain`; golden-строка в
`TestTemporalSegmentArgsRendersAHeldSegmentAsAFrozenClip` обновлена соответственно. Ни один
сценарий в `test/fixtures/corpus/*` не содержит hold-сегмента, так что `pass-a-jobs.json`
golden-фикстуры этот фикс не затронул.

### Task 5: сквозная проверка на реальной записи — DONE

**Файлы:** нет новых, ручная/скриптовая проверка.

Работа:
- [x] Взять `session.json`+`voice.json`+`raw.mp4` из
      `~/take5-output/2026-08-22-113521/` (уже собранная реальная запись,
      использованная для расследования этого инцидента), пересчитать `project.json`
      через обновлённый `internal/director`, пересобрать `demo.mp4` через обновлённый
      `internal/render`.
- [x] `ffprobe demo.mp4` — видео- и аудио-дорожки одинаковой длины (в пределах кадра).
- [x] Визуально сверить несколько кадров у аннотаций/voice-cue с тем, что происходит на
      экране (тот же метод, что использовался в расследовании — извлечение кадров в
      конкретные output-моменты и сверка с ожидаемым действием).
- [x] Убедиться, что число сегментов в новом `project.json` для этой записи заметно
      меньше 56 (ожидание — печать теперь один сегмент вместо ~45).
- [x] Задокументировать результат прямо в этом файле плана (факт/цифры), не в отдельном
      файле.

**Результат:**

- `project.json`: 56 → **18** сегментов таймлайна (`take5 analyze`).
- `demo.mp4`: video 1771 кадров @ 60fps = 29517мс, audio (AAC) = 29503мс — расхождение
  ~14мс, в пределах одного кадра (16.7мс at 60fps). Контейнерная длительность обеих
  дорожек — 29.516667с.
- Визуальная сверка кадров на t=16000мс (бейдж "Enter" активен, ожидаемый диапазон по
  `project.json` — 15590–17190мс) и t=26000мс (бейдж "initial" активен, ожидаемый
  диапазон — 25268–26868мс) — оба совпали с ожидаемым содержимым экрана.
- **Найден и исправлен реальный баг при этой проверке**, не покрытый golden-тестами
  (которые сравнивают только сгенерированную строку filter graph, не реальный прогон
  FFmpeg): `tpad=stop_mode=clone:stop_duration=…`, применённый к single-frame-потоку
  сразу после `trim`, без явного повторного `fps=` перед `tpad`, молча добавлял **0**
  кадров вместо ожидаемого паддинга — `tpad` не может вычислить число кадров по
  duration без известного (явно навязанного) frame rate у входного потока, а поток из
  одного кадра после `trim` не несёт этой информации сам по себе. Voice-freeze сегменты
  (`HoldMs > 0`) из-за этого схлопывались до 1 кадра вместо запланированной длины — на
  тестовой записи это ~9.3с из двух freeze-сегментов (5736мс + 3540мс). Пофикшено
  добавлением `fps=%d` перед `tpad` в `segmentFilterChain` (`internal/render/
  temporal_renderer.go`) — см. golden/unit-тесты в `temporal_renderer_test.go`. Пойман
  именно благодаря новому post-Pass-A duration guard (Task 4) — без него это ушло бы в
  Pass B молча, как и оригинальный инцидент.

### Task 6: [Final] прогон полного набора тестов и обновление документации — DONE

- [x] `make prep` — зелёный.
- [x] Обновить `CLAUDE.md`, если появились новые устойчивые конвенции (например, если
      `internal/render` теперь тоже держит собственный небольшой "encode segment + concat
      demuxer" паттерн — стоит упомянуть рядом с уже описанным для `internal/plate`, если
      он действительно переиспользуется буквально, а не только по духу — при реализации
      решить, оправдан ли общий helper между `internal/plate` и `internal/render`, или
      дублирование (как уже сознательно сделано между `internal/render`/`internal/transcribe`/
      `internal/voiceover`'s `resolveBin` — см. CLAUDE.md "Conventions") предпочтительнее).
- [x] Переместить этот план в `docs/plans/completed/`.

## Definition of Done

- [x] Все 6 задач и их gate'ы выполнены.
- [x] `make prep` завершается с кодом 0.
- [x] Печать — один сегмент таймлайна независимо от длины текста; по-клавишный SFX/курсор
      не регрессировали.
- [x] `click` несёт реальный `startMs`/`endMs`.
- [x] `TypingConfig.CollapseToMs`/`classifyTypingGapWithBeat`/`computeTypingOverrides`
      удалены без замены на другую эвристику.
- [x] Pass A кодирует сегменты по отдельности и склеивает через concat demuxer; регрессионный
      тест на 56+ сегментов зелёный.
- [x] Post-Pass-A duration guard есть и покрыт тестом (искусственное расхождение →
      понятная ошибка, не тихий проход в Pass B).
- [x] Реальная запись `2026-08-22-113521` пересобрана и подтверждена как визуально
      синхронная (Task 5).
- [x] Golden fixtures отражают новое поведение осознанно (не автогенерация вслепую).

## Post-Completion

**Ручная проверка** (после реализации, не автоматизируется в этом плане):
- Записать новую живую демку с активной печатью, кликами, drag и голосовой озвучкой через
  extension (не синтетические фикстуры) и убедиться, что итоговый `demo.mp4` не
  воспроизводит инцидент.
- Оценить на глаз, не ощущается ли новая фиксированная скорость печати (`TypingConfig.Speed`)
  слишком быстрой/медленной на реальном тексте разной длины — при необходимости
  скорректировать константу (это единственный tunable этого плана, намеренно не
  автоматизирован).
