# План — сегментированная сборка `raw.mp4` во время записи

Status: approved design, implementation not started.

## Цель

Сократить пиковое место, занимаемое `frames/*.jpg`, не меняя extension protocol и не
ухудшая результат существующего pipeline. Extension по-прежнему отправляет timestamped JPEG,
но native host во время записи собирает их в проверенные 20-секундные H.264/MP4-сегменты и
удаляет только уже заменённые ими JPEG.

После `session-stop` все сегменты соединяются без повторного кодирования video, к ним при
наличии добавляется `voice.webm`, атомарно публикуется единый `raw.mp4`, после чего продолжается
текущий pipeline `voice -> analyze -> render -> compact`.

## Критерии успеха

- При исправном FFmpeg после завершения очередного encode в `frames/` остаются только JPEG
  активного (ещё не закрытого) batch и очередь ещё не закодированных batches.
- Обычная запись не хранит все JPEG до `session-stop`; для длинной записи основной объём на
  диске составляют H.264-сегменты.
- `raw.mp4` сохраняет текущие duration, viewport, 60 fps plate, H.264 CRF 18 и синхронизацию с
  `voice.webm`.
- Video каждого JPEG кодируется один раз. Финальная сборка использует stream copy для video.
- Ни `.part`, ни непроверенный segment не позволяют удалить исходные JPEG.
- Сбой FFmpeg не прерывает capture: запись продолжает сохранять JPEG и остаётся
  восстанавливаемой.
- После crash можно использовать проверенные segments и JPEG tail, не начиная обработку всей
  записи заново.
- При уже существующем валидном `raw.mp4` повторный `render` не пересобирает plate.

## Не входит в scope

- Изменение extension, native-messaging protocol или частоты захвата.
- Переход на `Page.startScreenRecording`, WebCodecs или MediaRecorder для video.
- Настраиваемый размер окна, параллельные FFmpeg workers или adaptive CRF.
- Изменение Director, temporal rendering и формата `project.json`.
- Сохранение совместимости с незавершёнными session directories старого формата.

## Выбранная архитектура

Создать `internal/plate` как deep module. Его небольшой interface скрывает batching, background
worker, FFmpeg commands, atomic publication, recovery и cleanup:

```go
recorder, err := plate.Open(dir, logf)
err = recorder.AppendFrame(tMs, jpeg)
result := recorder.Finalize(durationMs, voicePath)
err = recorder.Abort(durationMs)

rawPath, err := plate.EnsureRaw(dir, logf)
```

Названия и точные Go-типы можно уточнить во время реализации, но interface не должен выдавать
наружу queue, windows, segment filenames или FFmpeg arguments. `session.Writer` владеет
events, network, audio и `session.json`; физическим lifecycle video artifacts владеет
`internal/plate`.

```text
extension JPEG
      |
      v
session.Writer.AppendFrame
      |
      v
plate.Recorder
  |-- active ~20 s batch: frames/*.jpg
  |-- one background FFmpeg worker
  `-- committed source: segments/NNNNNN-<start>-<end>.mp4
                              |
session-stop                 v
  tail encode -> concat -c copy -> audio mux -> raw.mp4
                                                |
                                                v
                              voice -> analyze -> render -> compact
```

FFmpeg и filesystem являются local-substitutable dependencies. Для production используются
реальные binaries/filesystem; tests проходят через interface module с fake executables и
temporary directories. Не создавать публичный port только ради mock: существующих
`FFMPEG_PATH`/`FFPROBE_PATH` достаточно как internal seam.

Сознательно упрощено относительно первой версии этого плана (три решения ниже применяются
во всех соответствующих разделах и tasks):

- **Нет отдельного manifest-файла.** `plate-state.json` не заводится. Committed segment — это
  файл `segments/NNNNNN-<startMs>-<endMs>.mp4` без суффикса `.part`; state — это листинг
  директории, а не JSON, который нужно публиковать атомарно отдельно от самих файлов. Убирает
  целый failure mode («segment переименовался, но не попал в manifest») по построению.
- **Нет `ffprobe` на каждый segment.** Существующий `AssemblePlate` уже не пробирует
  `raw.mp4` после кодирования — доверяет exit-коду FFmpeg и atomic rename; финальный
  `raw.mp4` пробируется один раз вызывающей стороной (`postproduction.Render`), как и
  сейчас. Segment-level FFmpeg-run + atomic rename дают ту же гарантию «ни один непроверенный
  файл не виден под финальным именем», без per-segment subprocess.
- **Нет выравнивания на абсолютную сетку окон.** Вместо `[0,20000)`, `[20000,40000)` batch
  копится, пока не наберётся ~20s session-time от начала batch. Длинная статичная пауза не
  требует synthetic multi-window hold intervals — она становится одним долгим hold-segment,
  что одновременно проще и дешевле.

## Артефакты session directory

Во время записи:

```text
<session>/
  frames/
    000123-000000019850.jpg
    000124-000000020017.jpg
  segments/
    000001-000000000000-000000020033.mp4
    000002-000000020033-000000041200.mp4.part
  voice.webm
  debug.log
```

- Имя JPEG содержит sequence и `tMs`. Это позволяет восстановить tail после аварийного
  завершения без перезаписи растущего JSON на каждый frame.
- JPEG сначала пишется как `<name>.part`, затем атомарно переименовывается. Recovery игнорирует
  незавершённые `.part`.
- Segment пишется как `NNNNNN-<startMs>-<endMs>.mp4.part` с явным `-f mp4`, затем атомарно
  переименовывается в `NNNNNN-<startMs>-<endMs>.mp4`. Имя — единственный источник state:
  committed segment — это любой файл `segments/*.mp4` без `.part`; `[startMs,endMs)` читается
  прямо из имени. Отдельный manifest не заводится, поэтому нет состояния, которое могло бы
  разойтись с файловой системой.
- Файл segment считается committed только после успешных encode и rename. До этого его JPEG
  нельзя удалять.

После успешной финализации остаются текущие долговечные артефакты:

```text
<session>/
  raw.mp4
  voice.webm        # если записывался; не удаляется
  session.json
  debug.log
  ...pipeline outputs
```

`frames/` и `segments/` удаляются только после проверки опубликованного `raw.mp4`.

## Семантика batch-накопления

Каждый пришедший frame принадлежит ровно одному batch — batches партиционируют frame sequence,
без дублирования кадров между ними. Это делает результат конкатенации сегментов побитово
эквивалентным сегодняшнему single-pass `BuildConcatList` над теми же frames, без отдельного
carry-frame механизма:

- Batch — это открытый, ещё не переданный worker'у диапазон `[batchStartMs, ...)`, куда попадают
  все новые frames. Он копится, пока очередной пришедший frame не окажется на расстоянии ≥20s
  session-time от `batchStartMs`.
- Frames ожидаются в неубывающем `tMs`, но CDP screencast на практике иногда присылает `tMs`
  на несколько миллисекунд раньше предыдущего кадра (подтверждено реальной записью, не
  гипотетически). Это не ошибка данных, а джиттер захвата: такой frame принимается, а его
  `tMs` для всей внутренней бухгалтерии (batch/имя файла) клэмпится до `tMs` предыдущего кадра
  — что concat list уже умеет превращать в нулевой/минимальный hold. Ранняя версия этого плана
  здесь возвращала явную ошибку и рвала всю запись; это было исправлено после того, как именно
  так и произошло на реальной записи.
- Frame, из-за которого расстояние превысило порог, закрывает текущий batch (его самого в
  закрытый batch не включает) и становится первым frame нового batch — его `tMs` и есть
  `batchStartMs` нового batch. Закрытый batch's последний реальный frame держится (repeat-last
  entry в его собственном concat list, как сегодня в `BuildConcatList`) до `tMs` этого нового
  frame — то есть ровно та длительность, которую этот frame получил бы в едином, неразбитом
  проходе. Отдельная copy изображения между batches не нужна.
- Batch не привязан к абсолютной сетке: если между frames прошла длинная статичная пауза, он
  просто остаётся открытым (ничего не пишется, `frames/` не растёт) до следующего реального frame
  или `session-stop` — тогда закрывается одним batch, покрывающим всю паузу, а не серией
  synthetic окон.
- Последний, ещё не закрытый batch на `Finalize` закрывается значением `durationMs` из
  `session-stop` — тем же repeat-last-entry механизмом, что и сегодняшний tail.
- Первый batch без единого frame является ошибкой «this session recorded no frames», как и
  сейчас.
- JPEG удаляется, как только его batch закодирован и переименован — ни один JPEG не живёт
  дольше своего собственного (закрытого) batch, кроме тех, что всё ещё в открытом активном
  batch.

Все segments обязаны иметь одинаковые codec parameters: H.264 (`libx264`), `yuv420p`, 60 fps,
одинаковые even width/height, time base и совместимые codec extradata. Это load-bearing
условие для финального concat через `-c copy`.

## Порядок commit одного segment

1. Закрыть immutable batch `[startMs,endMs)`; frame, вызвавший закрытие, уходит в следующий
   batch, не в этот.
2. Передать batch единственному background worker.
3. Построить concat list из frames самого batch с локальными timestamps (первый — от
   `startMs`, остальные — от своих `tMs`, последний держится до `endMs`).
4. Закодировать `segments/NNNNNN-<startMs>-<endMs>.mp4.part` текущими settings:
   `scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=60`, `libx264`, `veryfast`, CRF 18, `yuv420p`.
5. Атомарно переименовать `.mp4.part` в `segments/NNNNNN-<startMs>-<endMs>.mp4`. Rename — это
   единственный commit point: имя файла уже несёт `[startMs,endMs)`, поэтому нет отдельного
   шага «записать manifest».
6. Удалить все JPEG этого batch — следующий batch начинается со своего собственного frame
   (уже сохранённого на диске отдельно), ничего из этого batch ему не нужно.

Один worker выбран намеренно: он ограничивает CPU, сохраняет ordering и не требует scheduler.
Если encode отстаёт, immutable batches остаются в очереди вместе со своими JPEG. Это временно
увеличивает disk usage, но не теряет frames и не блокирует native message intake.

## Финализация `raw.mp4`

На `session-stop`:

1. `session.Writer` flush/close выполняет для `voice.webm`, чтобы FFmpeg видел полный файл.
2. `plate.Recorder.Finalize` запрещает новые frames и ждёт текущий worker.
3. Tail кодируется теми же settings и проходит тот же commit sequence.
4. Committed segments (найденные листингом `segments/*.mp4`, отсортированные по seq из имени)
   соединяются concat demuxer через `-c:v copy` во временный video-only MP4. Video повторно не
   кодируется.
5. Если `voice.webm` существует, video копируется, а Opus audio преобразуется в AAC 160 kbps.
   Без voice временный video используется как итоговый container.
6. Результат обрезается до `durationMs`, проверяется `ffprobe` и публикуется атомарным rename
   как `raw.mp4`.
7. Только после успешного probe удаляются frames, segments и временные concat files.
8. `session.Writer.Finalize` завершает metadata; host запускает существующий post-production
   pipeline.

Публикация обязана происходить в том же filesystem, что и session directory, чтобы rename был
атомарным. Если output имеет суффикс `.part`, FFmpeg получает явный container format.

## Ошибки и recovery

### Ошибка background encode

- Capture продолжается и сохраняет JPEG.
- Module запоминает первую encode error, прекращает удаление JPEG и пишет причину в
  `debug.log` один раз.
- На `Finalize` выполняется одна повторная попытка собрать недостающие segments. Не вводить
  бесконечные retries.
- Если повтор неуспешен, session metadata всё равно финализируется, а исходники сохраняются.
  Ошибка plate остаётся задачей post-production, а не превращает законченную запись в
  `recording_failed`.

### `Abort` или disconnect до `session-stop`

- Новые frames больше не принимаются.
- Текущий worker завершается; валидные committed segments сохраняются.
- `session.json` получает implied duration по последнему frame/event, как сейчас.
- JPEG tail и segments остаются для ручного или последующего recovery.
- Никакая финальная сборка не маскирует исходную причину abort.

### Повторный `render`

`postproduction.Render` вызывает `plate.EnsureRaw`:

- валидный `raw.mp4` возвращается без изменений;
- `.part` игнорируются;
- committed segments перечисляются листингом `segments/*.mp4` (без `.part`), `[startMs,endMs)`
  разбирается из имени, и повторно не кодируются;
- tail восстанавливается из timestamped JPEG filenames;
- отсутствующие или невалидные (не проходящие contiguity-проверку) segments пересоздаются
  только при наличии их JPEG source;
- после получения и проверки `raw.mp4` выполняется idempotent cleanup.

Если восстановить непрерывный plate невозможно, возвращается ошибка с именем первого
отсутствующего interval/artifact. Нельзя молча выпустить укороченное video.

## План реализации

### Task 1 — зафиксировать нынешний plate contract

Файлы:

- `internal/render/plate.go`
- новые characterization tests рядом с текущими render tests

Работа:

- [ ] Зафиксировать текущие `PlateDurationMs` и hold-last-frame rules.
- [ ] Зафиксировать 60 fps, even dimensions, H.264 CRF 18 и optional AAC mux.
- [ ] Добавить случаи: frame на границе, длинная статичная пауза, неполный tail, no frames,
      voice/no voice.
- [ ] Проверять наблюдаемый `raw.mp4` через `ffprobe`, а не внутренние FFmpeg arguments там,
      где доступен настоящий binary.

Gate: новые tests зелёные до переноса implementation.

**Статус: не выполнялось.** Реализация пошла прямо на новый `internal/plate` без отдельного
шага characterization-тестов поверх старого `internal/render/plate.go` перед его удалением.
Старый файл уже удалён, так что делать этот Task 1 задним числом больше нет смысла — если
нужна отдельная проверка «старое и новое поведение совпадает», её придётся строить как
сравнение golden output, а не как задуманный здесь gate.

### Task 2 — создать deep module `internal/plate`

Файлы:

- `internal/plate/recorder.go`
- `internal/plate/segment.go`
- `internal/plate/ffmpeg.go`
- `internal/plate/recorder_test.go`

Работа:

- [x] Ввести маленький interface `Open`, `AppendFrame`, `Finalize`, `Abort`, `EnsureRaw`.
- [x] Перенести plate-specific timing/concat/FFmpeg logic из `internal/render/plate.go`.
- [x] Оставить FFmpeg process details внутри implementation.
- [x] Ввести timestamped atomic JPEG writes (`<seq>-<tMs>.jpg` через `.part`+rename) и atomic
      segment writes (`.mp4.part`+rename в имя, несущее `[startMs,endMs)`). Без отдельного
      manifest-файла.
- [x] Не добавлять generic job queue, repository interface или config framework.

Gate: tests вызывают тот же interface, что `session.Writer` и `postproduction.Render`. Выполнено —
`internal/session/writer.go` и `internal/postproduction/postproduction.go` оба используют только
`Open`/`AppendFrame`/`Finalize`/`Abort`/`EnsureRaw`.

### Task 3 — реализовать windowing и background worker

Файлы:

- `internal/plate/recorder.go`
- `internal/plate/segment.go`
- `internal/plate/recorder_test.go`

Работа:

- [x] Партиционировать ordered frames на batches: frame, из-за которого разрыв от начала batch
      достиг ~20s, закрывает текущий batch и открывает следующий.
- [x] Передавать immutable batches одному worker goroutine.
- [x] Длинная static-пауза без новых frames держит batch открытым, не закрывая его синтетически;
      закрывается одним hold-segment на первом реальном frame или на `Finalize`.
- [x] Commit segment выполнять только в порядке encode -> rename -> cleanup JPEG.
- [x] При первой worker error переходить в lossless degraded mode без остановки capture.
- [x] На `Finalize` дождаться worker и обработать tail; на `Abort` сохранить recovery state.

Gate: table-driven tests доказывают interval coverage без gaps/overlaps и отсутствие удаления
JPEG на каждой смоделированной точке отказа. **Частично**: `TestRecorderEndToEndProducesRawMp4`
проверяет multi-segment coverage end-to-end (реальный FFmpeg) и `TestAbortPreservesRecoveryArtifactsWithoutBuildingRaw`
проверяет отсутствие удаления JPEG на одном конкретном сценарии (Abort до закрытия batch) — но
это не полноценная table-driven матрица «на каждой смоделированной точке отказа», которую
описывает gate; degraded-mode путь (worker error → JPEG не удаляются) не покрыт отдельным
тестом с искусственно сбойным FFmpeg.

### Task 4 — собрать и проверить `raw.mp4`

Файлы:

- `internal/plate/finalize.go`
- `internal/plate/ffmpeg.go`
- `internal/plate/recorder_test.go`

Работа:

- [x] Segments совместимы по построению (все закодированы этим же module с одними и теми же
      settings) — отдельная compatibility-проверка перед concat не нужна.
- [x] Соединять video через `-c:v copy`.
- [x] Добавлять `voice.webm` как AAC 160 kbps без video re-encode.
- [x] Ограничивать итог точным session duration.
- [x] Публиковать только проверенный `raw.mp4` через atomic rename.
- [x] После успеха выполнять idempotent cleanup source artifacts.

Gate: real-FFmpeg test подтверждает один H.264 video stream, optional AAC stream, ожидаемые
dimensions/fps/duration и отсутствие второго video encode на финальном шаге. **Частично**:
`TestRecorderEndToEndProducesRawMp4` реальным FFmpeg проверяет multi-segment video (probe +
frames/segments cleanup), но ни один test не прогоняет случай с `voice.webm`, то есть AAC-поток
в собранном `raw.mp4` фактически не проверен автотестом.

### Task 5 — подключить module к recording lifecycle

Файлы:

- `internal/session/writer.go`
- `internal/host/host.go` только если требуется wiring/error classification
- существующие и новые tests `internal/session`/`internal/host`

Работа:

- [x] `session.Writer.Open` открывает `plate.Recorder`.
- [x] `AppendFrame` делегирует physical storage module, но сохраняет counters/logging.
- [x] Перед plate finalization закрывать `voice.webm`.
- [x] Plate encode error логировать как recoverable post-production error.
- [x] Сохранить текущие `Finalize`/`Abort` semantics для events, network и session metadata.
- [x] Не менять native-messaging messages и extension lifecycle.

Gate: существующие host protocol tests проходят без изменений protocol fixtures; новые tests
подтверждают, что slow worker не меняет ordering принимаемых frame/event/audio messages.
**Частично**: все существующие `internal/host` tests зелёные без изменений protocol fixtures, и
добавлен `TestRunProducesRawMp4FromRealFrames` (реальные frames -> `raw.mp4` через `Run()`) — но
нет теста, который бы искусственно тормозил worker и проверял, что приём frame/event/audio
сообщений при этом не блокируется; это осталось не проверенным, только гарантировано архитектурой
(unbounded queue).

### Task 6 — подключить recovery к post-production

Файлы:

- `internal/postproduction/postproduction.go`
- `internal/postproduction/postproduction_test.go`
- удалить заменённый `internal/render/plate.go` после переноса callers/tests

Работа:

- [x] Заменить `render.AssemblePlate` и `compactFrames` одним `plate.EnsureRaw`.
- [x] Сделать `EnsureRaw` idempotent для complete, partial и recovered sessions.
- [x] Не удалять `voice.webm`.
- [x] Сохранить порядок `ensure raw -> voice -> analyze -> render`.
- [x] Удалить старую frames-only implementation и tests, которые проверяют её внутренности;
      поведение проверять через новый interface.

Gate: post-production tests покрывают valid raw, segments + tail, JPEG-only fallback, failed
probe и cleanup failure. **Частично**: есть valid-raw fast-path
(`TestRenderEndToEndWithExistingPlateSkipsAssembly`), JPEG-only fallback
(`TestEnsureRawRecoversFromLeftoverFramesAfterACrash`, в `internal/plate`) и failed-recovery
(`TestEnsureRawFailsLoudlyWhenAGapHasNoSourceFrames`). Нет теста именно на «уже есть часть
committed segments + tail из JPEG» (только «всё из JPEG с нуля»), и нет теста на cleanup failure
(`os.RemoveAll` не срабатывает после публикации `raw.mp4`).

### Task 7 — crash/failure matrix

- [ ] Crash во время записи JPEG `.part`.
- [ ] Crash во время segment `.mp4.part`.
- [x] Crash после segment rename, но до JPEG cleanup.
- [ ] FFmpeg отсутствует при записи и появляется перед повторным `render`.
- [ ] FFmpeg возвращает ошибку один раз, затем recovery проходит.
- [ ] Committed segment не проходит concat (или итоговый `ffprobe`) при ещё не удалённых его
      JPEG (worker прервался между rename и cleanup) — пересобирается из них.
- [ ] То же самое, но JPEG уже удалены (нормальный post-cleanup случай) — явная ошибка вместо
      truncated `raw.mp4`.
- [ ] Повторные `Finalize`, `Abort` и `EnsureRaw` не удаляют единственную валидную копию.

Gate: каждый сценарий проверяет только observable files/result/error через interface module.

**Статус: частично, по факту реального инцидента, не по плану.** Ни один из восьми сценариев
не был написан заранее как test-first матрица. Но 2026-08-22 реальная запись подтвердила ровно
один из них не в тесте, а в проде: процесс убило (см. ниже) в узком окне между «ffmpeg
закончил encode segment 9» и `os.Rename` — то есть «crash после encode, до rename» с ещё не
удалёнными JPEG этого batch. Данные восстановились штатно (committed segments 1-8 + JPEG-хвост
из batch 9+10), но обнажили два реальных пробела, которые эта матрица должна была поймать
заранее и не поймала:

1. **`EnsureRaw` требовал `session.json`**, а процесс погиб до того, как `session.Writer` хоть
   раз вызвал `Abort`/`Finalize` — то есть `session.json` не существовал вообще. Исправлено:
   при отсутствующем `session.json` длительность выводится из последнего committed segment /
   последнего frame (`inferDurationMs`), с явным логом об этом.
2. **Сам процесс не ловил `SIGTERM`/`SIGINT`/`SIGHUP`.** `cmd/spike-host` (SPIKE.md-прототип)
   ещё на этапе исследования отслеживал эти сигналы, а в реальный `cmd/take5 host` это
   так и не перенесли — на них действовала стандартная диспозиция Go (мгновенный exit, ноль
   cleanup, отсюда полное отсутствие `"aborted: ..."` в `debug.log`). Исправлено: `host`
   ловит эти сигналы и закрывает `os.Stdin`, что `internal/host.Run` теперь трактует как
   штатное `EOF` (запускает `Abort`, пишет `session.json`) — то же самое, чем сегодня
   является обычное закрытие порта Chrome.

Остальные семь сценариев матрицы по-прежнему не покрыты отдельными тестами.

**Observability, найденная тем же разбором.** У процесса не было ни одного durable log
channel, кроме per-session `debug.log` (который начинает существовать только после
`session-start`). Всё остальное — `logger.Printf`/`logger.Fatalf` в `cmd/take5` —
шло только в stderr, а Chrome его никуда не сохраняет (SPIKE.md §5, это уже было
задокументировано, но вывод из этого не был сделан системно). Это никак не связано с
segmented plate как таковым, но всплыло из того же инцидента, поэтому фиксирую здесь:

- `cmd/take5 host` теперь пишет весь `logger` вывод не только в stderr, но и в
  `<outputDir>/host.log` (append-only, переживает весь host, не только одну сессию) —
  закрывает разрыв для всего, что происходит до `session-start` или вообще без него.
- Ни одна goroutine нигде в кодовой базе не имела `recover()` — паника в фоновом
  `plate.Recorder.worker()` (или где угодно ещё в синхронном пути `host.Run`) убивала весь
  процесс без единой строки, которую можно было бы увидеть. Добавлены `withRecover` в
  `internal/plate` (паника в encode одного batch превращается в обычную degraded-ошибку, не
  в смерть всего процесса) и recover-обёртка вокруг `host.Run` в `cmd/take5` (паника
  где угодно ещё в этом пути теперь оставляет stack trace в `host.log` вместо тишины). Важная
  оговорка: `recover()` не спасает саму запись — он не даёт одному сбою (например, в encode
  одного batch) уронить всё остальное, но если процесс всё-таки погибает (SIGKILL не
  перехватить в принципе), то, что уже произошло, всё равно нужно было сохранить *раньше* —
  см. следующий пункт, это и есть настоящее предотвращение, а не просто более громкая ошибка.
- **Настоящая причина, по которой `session.json` вообще мог отсутствовать: `events`/`network`
  жили только в памяти `session.Writer` и писались на диск один раз, в самом конце
  (`Finalize`/`Abort`).** Асимметрично с frames, которые как раз ради этого весь план и делает
  durable по одному кадру. Первая версия фикса переписывала весь `session.json` на каждый
  `AddEvent`/`AddNetworkRecord` — рабочая, но O(n) на событие (O(n²) суммарно) и привязанная к
  тому, что `session.json` — это единый JSON-документ, который для инкрементальной записи
  вообще не подходящий формат. Финальная версия: `internal/session.JournalFile`
  (`journal.jsonl`) — append-only, одна JSON-строка на событие/network record, тот же паттерн,
  что уже был у `debug.log` и у `frames/`/`segments/` в этом плане (durable маленькими
  кусками, никогда не переписывается целиком). `session.json` пишется всего дважды за
  нормальную запись: один раз в `Open` (`sessionId`/`url`/`viewport`, чтобы хоть что-то
  пережило смерть до первого события) и один раз в `Finalize`/`Abort` (полный, отсортированный
  документ) — happy path побайтово не изменился. `internal/session.Recover(dir)` реплеит
  `journal.jsonl` и «лечит» `session.json` на месте (атомарно), только если в нём ещё нет
  своих `events`/`network` — то есть только тогда, когда `Finalize`/`Abort` так и не
  отработали; `postproduction.Render` вызывает его перед `Analyze`. Побочный эффект: раз
  `AppendFrame` в журнал не пишет (кадры и так уже durable через `internal/plate`, а писать
  событие на каждый кадр — до 60 раз в секунду — было бы чистым расточительством),
  восстановленный `durationMs` может отставать от реально записанного видео, если кадры
  продолжали приходить уже после последнего события. `plate.EnsureRaw` поэтому берёт `max`
  между `session.json`'s `durationMs` и выведенным из `segments`/`frames` значением, а не
  слепо доверяет ему — иначе устаревший `durationMs` тихо обрезал бы хвост восстановленного
  `raw.mp4`.
- **Третий инцидент оказался не багом в этом коде вообще — macOS убивала процесс через свой
  disk-I/O rate governor — ПЕРВОНАЧАЛЬНЫЙ диагноз, впоследствии опровергнутый.** Системные
  логи (`log show`) показывали `kernel: process take5[...] caught waking the CPU
  45001 times over ~20 seconds ... violating a limit of 45000 wakes over 300 seconds` и
  `kernel: Coalition [...] caught causing excessive I/O` при каждом инциденте. Убрал rename
  для кадров (`internal/plate/recorder.go` — пишем сразу под финальным именем, один syscall
  вместо двух; `writeFileAtomic` удалён как более не нужный в проде) в качестве первого
  ответа. **Это не помогло** — третий инцидент дал практически идентичные числа (`2262
  wakes/sec` против `2228` — в пределах шума), несмотря на убранный syscall. Проверка на
  Apple Developer Forums (DTS-инженер, официально) это объяснила:
  **wakes-предупреждение чисто диагностическое, `Action taken: none`, процесс из-за него не
  убивают.** То же самое, судя по всему, верно и для disk-writes/`excessive I/O` сообщений —
  spindump-репорт для них тоже содержал `Action taken: none`. Другими словами, оба замера в
  логах, на которые изначально списали три сорванные записи, — красная селёдка.
  Подтверждено ещё раз независимо: прогнал те же настоящие кадры через **весь** `host.Run()`
  pipeline (не только `internal/plate`, а с JSON+base64, как реально шлёт Chrome) отдельным
  процессом из терминала — 25s, 1806 сообщений, никаких предупреждений в `log show` вообще.
  То есть тот же код с теми же данными не воспроизводит проблему, когда его не спавнит
  Chrome — сильный сигнал, что дело не в эффективности этого кода как такового.
  Настоящая причина осталась неустановленной: во всех трёх инцидентах `debug.log` обрывается
  без строки `aborted: ...`, которую дал бы штатный EOF-путь при закрытии порта Chrome — то
  есть это не обычное отключение расширения, а что-то более резкое. Добавлен heartbeat
  (`cmd/take5/main.go`, каждые 5s в `host.log`, тот же паттерн, что уже был в
  `cmd/spike-host`) — при следующем инциденте это отделит «процесс завис» (heartbeat не
  прерывается) от «процесс убили» (heartbeat обрывается ровно там же, где всё остальное).
  Бинарник также теперь подписывается с hardened runtime (`make build`, `codesign --options
  runtime`) вместо голого linker-adhoc — не подтверждено, что это на что-то влияет, но
  дёшево и не помешает.

  **Найдена настоящая причина, четвёртым заходом.** Heartbeat в следующем же инциденте показал:
  процесс не завис — он был жив (heartbeat каждые 5s) до конкретной секунды, затем PID пропал
  (`ps -p` пусто) в течение следующих 5s. Не hang, убийство. Пользователь подтвердил, что сам
  нажал «стоп» — и именно это совпадение оказалось ключом. Chromium's own native-messaging
  contract: *"Once the Port is disconnected the browser will give the process a few seconds to
  exit gracefully, and then kill it if it has not exited."* А `plate.Recorder.Finalize`
  (старая версия) на `session-stop` делал именно то, что легко выходит за «несколько секунд»:
  ждал `stopWorker()` (фоновый FFmpeg мог быть на середине encode ~20s batch) и затем сам
  конкатенировал все сегменты + AAC + probe — синхронно, внутри живого host-процесса. Отсюда
  всё: ни один сигнал не пойман (это не SIGTERM), паники нет, `aborted:` нет (не тот путь),
  `finalized:` тоже нет (не успел). Мои более ранние реплеи ни разу это не воспроизвели именно
  потому, что вызывали `Finalize()` напрямую, без дедлайна Chrome.

  **Исправлено архитектурно, не патчем.** `plate.Recorder.Finalize`/`Abort` схлопнуты в один
  `Stop()` — синхронно ничего не ждёт и не собирает `raw.mp4`, просто помечает recorder
  остановленным и возвращается немедленно. Сборка `raw.mp4` теперь безусловно — всегда,
  не только при recovery — работа `plate.EnsureRaw`, вызываемая из уже существующего detached
  `render`-процесса (`spawnDetachedRender`, тот же путь, что `voice -> analyze -> render`).
  Всё, что фоновый worker не успел закодировать к моменту `Stop()`, просто остаётся на диске
  (сегмент не закоммичен, его JPEG никуда не делись) — `EnsureRaw` дособерёт это в detached
  процессе, без дедлайна Chrome, тем же путём, что уже использовался для восстановления после
  крэша. Живой e2e-прогон (25 кадров, честный `host` бинарник) подтвердил: процесс завершается
  за **0.42s** вместо неограниченного времени на полную сборку, `debug.log` получил строку
  `finalized: ...`, которой не было ни разу за три предыдущих инцидента, `demo.mp4` собрался
  корректно в фоне. Тесты `internal/plate`/`internal/host` переписаны под новый контракт
  (`Stop()` без параметров и без ожидания; `EnsureRaw` вызывается отдельно, как это теперь
  всегда и происходит).

  **Регрессионный тест на само свойство, не на результат.** Ни один из существующих тестов не
  поймал бы эту проблему заранее — все они проверяли *что* возвращает `Finalize`, ни один не
  проверял *сколько это занимает под нагрузкой*. Добавлен
  `TestStopReturnsImmediatelyRegardlessOfEncodeBacklog`: ставит в очередь несколько батчей
  реального FFmpeg-кодирования (заведомо больше, чем один воркер успеет разгрести), затем
  меряет `Stop()` с порогом 200ms. Проверено вручную, что тест ловит именно эту регрессию:
  временный откат `Stop()` к блокирующему ожиданию (`<-r.workerDone`) валит тест на `345ms`.

  **Добавлено подтверждение от хоста расширению.** `stopRecording()` раньше показывал
  «успех» сразу после отправки `session-stop`, не дожидаясь, что хост реально дообработал
  сессию — именно так один из инцидентов остался незамеченным пользователем. Хост теперь
  шлёт `{"type":"session-finalized",...}` после `Finalize`, extension ждёт его (таймаут 8s)
  перед тем как показать успех; если не дождался — явная ошибка в badge/notification.
  При первой проверке этого фикса `OnComplete` (spawn `render`) вообще перестал срабатывать —
  оказалось, что `WriteJSON` ack была вставлена ДО `OnComplete`, и что бы с ней ни происходило
  (расследование не завершено — эмпирически показано, что запись в мёртвый `os.Pipe()` в Go
  не роняет процесс, но реальный pipe от Chrome мог вести себя иначе), это стояло между
  критичной работой и её логом. Переставлено: `OnComplete` теперь строго до ack-сообщения,
  которое ни при каких обстоятельствах не может задержать или сорвать спавн `render`.
  Заодно добавлено: `internal/host` теперь логирует получение `session-stop` явно
  (`writer.Log("received session-stop")`) — раньше вообще ни одно из ИЗВЕСТНЫХ входящих
  сообщений (`session-start`/`frame`/`session-stop`/...) не логировалось, только неизвестные
  типы и отброшенные записи, что и затрудняло диагностику всех предыдущих раундов.

### Task 8 — измерить эффект на реальной записи

- [ ] Записать не менее пяти минут с активным scrolling/typing и статичными паузами.
- [ ] Во время записи измерить peak bytes отдельно для `frames/` и `segments/`.
- [ ] Подтвердить, что после того как worker догнал capture, JPEG не старше активного
      (ещё не закрытого) batch.
- [ ] Сравнить старый и новый `raw.mp4`: duration с допуском один frame, dimensions, fps,
      наличие audio и синхронизацию начала/конца.
- [ ] Прервать host в середине записи и успешно выполнить `render` из recovery artifacts.
- [ ] Проверить, что итоговый `demo.mp4` строится обычным pipeline.

Gate: функциональная проверка пройдена, а peak disk usage больше не растёт линейно как сумма
всех JPEG при worker, успевающем за capture.

**Статус: не выполнялось.** Требует реальной записи через extension/Chrome, которую я не могу
запустить сам в рамках этой сессии. Синтетические тесты в `internal/plate` подтверждают
корректность на уровне unit/integration (реальный FFmpeg, но фейковые кадры), но ни peak disk
usage, ни сравнение старого/нового `raw.mp4` на настоящей записи, ни `demo.mp4` через полный
pipeline не проверялись.

## Команды проверки

Во время разработки, после соответствующих tasks:

```sh
go test ./internal/plate ./internal/session ./internal/host ./internal/postproduction ./internal/render
make prep
```

Финальная ручная проверка выполняется с реальными `ffmpeg` и `ffprobe`; skip из-за их отсутствия
не считается выполнением Task 8.

## Риски и ограничения

- **FFmpeg медленнее capture.** Queue и JPEG временно растут. Сначала измерить реальную
  производительность; несколько workers не добавлять без подтверждённой необходимости.
- **Несовместимые MP4 segments.** Pin codec settings и проверять каждый segment до удаления
  JPEG. Не делать silent fallback с video re-encode.
- **Crash между filesystem operations.** Commit ordering и atomic rename гарантируют, что
  всегда остаётся JPEG source или committed segment.
- **Изменение viewport во время записи.** Если dimensions отличаются от первого segment,
  прекратить compaction и сохранить JPEG для явного recovery; отдельную resize policy не
  изобретать без воспроизводимого требования.
- **FFmpeg отсутствует.** Capture остаётся рабочим в JPEG degraded mode, как до изменения;
  `doctor` и последующий `render` сообщают исходную ошибку.
- **CPU/нагрев.** Один worker и preset `veryfast` ограничивают нагрузку. Дополнительный throttle
  вводить только после измерения.

## Definition of Done

- [ ] Все восемь tasks и их gates выполнены. **Нет** — Task 1 и Task 7 не выполнялись вообще,
      Task 8 требует реальной записи через extension (не выполнялось), Task 4/5/6 выполнены, но
      их gates покрыты тестами частично (см. пометки внутри каждого task).
- [x] `make prep` завершается с кодом 0.
- [ ] Реальная пяти-минутная запись проходит capture, finalization и полный pipeline. Не
      проверялось (см. Task 8).
- [x] В happy path session directory после finalization не содержит JPEG, segments или `.part`
      — подтверждено автотестами (`internal/plate`, `internal/host`), но не реальной записью.
- [ ] В failure path остаётся достаточно проверяемых artifacts для recovery. Подтверждено для
      одного сценария (`TestAbortPreservesRecoveryArtifactsWithoutBuildingRaw`), не для полной
      Task 7 матрицы.
- [x] Extension и native-messaging protocol не изменились.
- [x] Изменения оставлены незакоммиченными до отдельного явного подтверждения пользователя.
