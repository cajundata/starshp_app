import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { initPipeline, refreshReviewsDue } from './pipeline'

let activeConv: string | null = null
let streaming = false

type Usage = { input: number; output: number; cached: number; lastInput: number; lastOutput: number; modelID: string; stale: boolean }
const latestUsage = new Map<string, Usage>()
let usagePendingForConv: string | null = null  // set at send-start; cleared by chat:usage; if still set after send completes, mark stale

const fmt = (n: number) => n.toLocaleString('en-US')

function modelMaxContext(modelID: string, models: { id: string; maxContext?: number }[]): number {
  const m = models.find(x => x.id === modelID)
  return m?.maxContext ?? 0
}

let cachedModels: { id: string; display?: string; maxContext?: number }[] = []

type PersonaInfo = { id: string; name: string; model: string; color: string }
let cachedPersonas: PersonaInfo[] = []

const NEUTRAL_COLOR = '#8a8a90'

function personaById(id: string): PersonaInfo | undefined {
  return cachedPersonas.find(p => p.id === id)
}

// modelLabel is what the bubble's model chip shows: the display name the
// operator gave the model in models.yaml, falling back to the raw ID.
function modelLabel(modelID: string): string {
  const m = cachedModels.find(x => x.id === modelID)
  return m?.display || modelID
}

function updateFooter() {
  const el = $('ctxFooter')
  if (!activeConv) { el.classList.add('hidden'); el.textContent = ''; return }
  const u = latestUsage.get(activeConv)
  if (!u) { el.classList.add('hidden'); el.textContent = ''; return }
  const max = modelMaxContext(u.modelID, cachedModels)
  const prefix = u.stale ? '~' : ''
  const denom = max > 0 ? ` / ${fmt(max)}` : ''
  const occ = (Number.isFinite(u.lastInput) && Number.isFinite(u.lastOutput))
    ? u.lastInput + u.lastOutput
    : u.input
  const persona = personaSel.selectedOptions[0]?.text || ''
  const who = persona ? ` · ${persona}` : ''
  el.textContent = `context ${prefix}${fmt(occ)}${denom} · this turn ${fmt(u.input)}→${fmt(u.output)} · cache ${fmt(u.cached)}${who}`
  el.classList.remove('hidden')
}

const $ = (id: string) => document.getElementById(id) as HTMLElement
const thread = $('thread')
const input = $('input') as HTMLTextAreaElement
const personaSel = $('personaSel') as HTMLSelectElement
const sendBtn = $('sendBtn') as HTMLButtonElement
const mentionPopup = $('mentionPopup') as HTMLDivElement

let ragStatusEl: HTMLElement | null = null

function addMsg(role: string, text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = `msg ${role}`
  const txt = document.createElement('div')
  txt.className = 'msg-text'
  txt.textContent = text
  el.appendChild(txt)
  thread.appendChild(el)
  thread.scrollTop = thread.scrollHeight
  return el
}

const msgText = (el: HTMLElement) => el.querySelector('.msg-text') as HTMLElement

const COPY_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`
const CHECK_ICON = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`

// ---- Assistant run bubbles -------------------------------------------------
// An assistant turn is one "run". Its bubble is assembled from the chat:* event
// taxonomy: an optional grounding header, then an ordered sequence of text
// segments and inline tool-call blocks. Keyed by runID so live events and
// history (GetConversationDisplayEvents) build the same structure.

type RunBubble = {
  el: HTMLElement
  curText: HTMLElement | null // trailing text segment, or null after a tool block
  tools: Map<string, HTMLElement>
}
const runBubbles = new Map<string, RunBubble>()

function toolIcon(name: string): string {
  return name === 'search_textbook' ? '🔍' : name === 'safe_math' ? '🧮' : '🔧'
}

function argPreview(input: any): string {
  if (input == null) return ''
  let s: string
  try {
    s = typeof input === 'string' ? input : JSON.stringify(input)
  } catch {
    return ''
  }
  return s.length > 60 ? s.slice(0, 60) + '…' : s
}

function errCodeFromMeta(meta: any): string {
  return meta && typeof meta === 'object' && typeof meta.error_code === 'string' ? meta.error_code : ''
}

function ensureRunBubble(runId: string, personaId = '', modelId = ''): RunBubble {
  let b = runBubbles.get(runId)
  if (!b) {
    const el = document.createElement('div')
    el.className = 'msg assistant'
    thread.appendChild(el)
    thread.scrollTop = thread.scrollHeight
    b = { el, curText: null, tools: new Map() }
    runBubbles.set(runId, b)
  }
  applyAttribution(b, personaId, modelId)
  return b
}

// applyAttribution stamps the bubble with who spoke and on which model. Both
// the live path (chat:run_started) and the replay path (event.personaId /
// event.modelId) call it with the same two IDs, so a reopened conversation is
// colored identically to what the operator watched stream in.
//
// A run with no persona (recorded before personas existed) shows the model chip
// alone in a neutral color — honest about what is known, rather than inventing
// an assistant. A persona ID with no matching file (the operator deleted it)
// shows the literal ID, also neutral. Neither is an error.
function applyAttribution(b: RunBubble, personaId: string, modelId: string) {
  if (!personaId && !modelId) return
  if (b.el.querySelector('.msg-attrib')) return

  const p = personaId ? personaById(personaId) : undefined
  b.el.style.setProperty('--persona-color', p?.color || NEUTRAL_COLOR)
  if (personaId) b.el.dataset.persona = personaId

  const row = document.createElement('div')
  row.className = 'msg-attrib'

  if (personaId) {
    const dot = document.createElement('span')
    dot.className = 'persona-dot'
    const name = document.createElement('span')
    name.className = 'persona-name'
    name.textContent = p?.name || personaId
    row.append(dot, name)
  }
  if (modelId) {
    const chip = document.createElement('span')
    chip.className = 'model-chip'
    chip.textContent = modelLabel(modelId)
    row.appendChild(chip)
  }
  b.el.insertBefore(row, b.el.firstChild)
}

function appendRunText(runId: string, text: string) {
  if (!text) return
  const b = ensureRunBubble(runId)
  if (!b.curText) {
    b.curText = document.createElement('div')
    b.curText.className = 'msg-text'
    b.el.appendChild(b.curText)
  }
  b.curText.textContent += text
  thread.scrollTop = thread.scrollHeight
}

function addRunToolCall(runId: string, toolCallId: string, name: string, input: any) {
  const b = ensureRunBubble(runId)
  b.curText = null // text after a tool block starts a new segment
  const div = document.createElement('div')
  div.className = 'tool-call collapsed'
  div.dataset.toolCallId = toolCallId
  const icon = document.createElement('span')
  icon.className = 'tool-icon'
  icon.textContent = toolIcon(name)
  const nm = document.createElement('span')
  nm.className = 'tool-name'
  nm.textContent = name
  const arg = document.createElement('span')
  arg.className = 'tool-arg'
  arg.textContent = argPreview(input)
  const st = document.createElement('span')
  st.className = 'tool-status'
  st.textContent = '…'
  div.append(icon, nm, arg, st)
  div.onclick = () => div.classList.toggle('collapsed')
  b.el.appendChild(div)
  b.tools.set(toolCallId, div)
  thread.scrollTop = thread.scrollHeight
}

function updateRunToolResult(runId: string, toolCallId: string, isError: boolean, errorCode: string, latencyMs: number, summary: string) {
  const b = runBubbles.get(runId)
  if (!b) return
  const el = b.tools.get(toolCallId)
  if (!el) return
  const st = el.querySelector('.tool-status') as HTMLElement
  if (isError) {
    el.classList.add('errored')
    st.textContent = `error · ${errorCode || 'execution_error'}`
  } else {
    st.textContent = `· ${latencyMs || 0} ms`
  }
  if (summary) {
    const detail = document.createElement('div')
    detail.className = 'tool-summary'
    detail.textContent = summary
    el.appendChild(detail)
  }
}

function setRunGrounding(runId: string, status: string, sourceCount: number) {
  if (status !== 'ready') return
  const b = ensureRunBubble(runId)
  if (b.el.querySelector('.grounding-header')) return
  const h = document.createElement('div')
  h.className = 'grounding-header'
  h.textContent = `↳ grounded · ${sourceCount || 0} sources`
  const attrib = b.el.querySelector('.msg-attrib')
  if (attrib) b.el.insertBefore(h, attrib.nextSibling)
  else b.el.insertBefore(h, b.el.firstChild)
}

function setRunStatus(runId: string, status: 'completed' | 'errored' | 'cancelled') {
  const b = runBubbles.get(runId)
  if (!b) return
  if (status === 'errored') b.el.classList.add('status-errored')
  if (status === 'cancelled') {
    b.el.classList.add('status-cancelled')
    if (!b.el.querySelector('.cancelled-tag')) {
      const tag = document.createElement('div')
      tag.className = 'cancelled-tag'
      tag.textContent = 'cancelled'
      b.el.appendChild(tag)
    }
  }
  attachRunCopy(b)
}

// attachRunCopy adds a copy button that yields the concatenated text segments
// of the run (tool blocks excluded).
function attachRunCopy(b: RunBubble) {
  if (b.el.querySelector('.msg-actions')) return
  if (!b.el.querySelector('.msg-text')) return // nothing to copy (pure tool/error run)
  const row = document.createElement('div')
  row.className = 'msg-actions'
  const btn = document.createElement('button')
  btn.className = 'copy-btn'
  btn.title = 'Copy'
  btn.innerHTML = COPY_ICON
  let revertTimer: ReturnType<typeof setTimeout> | null = null
  btn.onclick = async () => {
    const text = Array.from(b.el.querySelectorAll('.msg-text'))
      .map((n) => n.textContent || '')
      .join('\n\n')
    try {
      await navigator.clipboard.writeText(text)
      if (revertTimer !== null) clearTimeout(revertTimer)
      btn.classList.add('copied')
      btn.innerHTML = CHECK_ICON
      revertTimer = setTimeout(() => {
        btn.classList.remove('copied')
        btn.innerHTML = COPY_ICON
        revertTimer = null
      }, 1500)
    } catch {
      // clipboard unavailable — no crash
    }
  }
  row.appendChild(btn)
  b.el.appendChild(row)
}

async function loadConversations() {
  const list = $('convList')
  list.innerHTML = ''
  const convs = (await App.ListConversations()) || []
  for (const c of convs) {
    const d = document.createElement('div')
    d.className = 'conv' + (c.id === activeConv ? ' active' : '')
    d.onclick = () => openConversation(c.id)

    const title = document.createElement('span')
    title.className = 'conv-title'
    title.textContent = c.title
    d.appendChild(title)

    const del = document.createElement('button')
    del.className = 'conv-del'
    del.textContent = '✕'
    del.title = 'Delete conversation'
    del.onclick = (e) => {
      e.stopPropagation()
      void deleteConversation(c.id)
    }
    d.appendChild(del)

    list.appendChild(d)
  }
}

async function deleteConversation(id: string) {
  // Deleting the conversation that is mid-stream would leave the in-flight
  // SendMessage writing to a deleted row — ignore the click until it finishes.
  if (streaming && id === activeConv) return
  if (!confirm('Delete this conversation? This cannot be undone.')) return
  try {
    await App.DeleteConversation(id)
  } catch (e: any) {
    alert(`Could not delete the conversation: ${e?.userMessage || e}`)
    return
  }
  if (id === activeConv) {
    activeConv = null
    thread.innerHTML = ''
    latestUsage.delete(id)
    updateFooter()
  }
  await loadConversations()
}

async function openConversation(id: string) {
  activeConv = id
  thread.innerHTML = ''
  runBubbles.clear()
  // History is the canonical display timeline: the active completed run per
  // turn (or the latest terminal run, so cancelled/errored partial output the
  // user saw is preserved). Each assistant event carries the persona and model
  // that produced it (joined from runs), so replayed bubbles are colored the
  // same as live ones. Token usage is not carried on events, so the footer
  // stays empty until the next live turn emits chat:usage.
  const events = (await App.GetConversationDisplayEvents(id)) || []
  for (const ev of events) {
    if (ev.kind === 'user_message') {
      addMsg('user', ev.text || '')
      continue
    }
    if (!ev.runId) continue
    // Create the bubble with its attribution before any content lands in it, so
    // a replayed run is colored exactly as the live one was.
    ensureRunBubble(ev.runId, ev.personaId || '', ev.modelId || '')
    if (ev.kind === 'assistant_text') {
      appendRunText(ev.runId, ev.text || '')
    } else if (ev.kind === 'assistant_tool_call') {
      addRunToolCall(ev.runId, ev.toolCallId || '', ev.toolName || '', (ev as any).toolInput)
    } else if (ev.kind === 'tool_result') {
      updateRunToolResult(
        ev.runId, ev.toolCallId || '', !!ev.isError,
        errCodeFromMeta((ev as any).toolMetadata), ev.toolLatencyMs || 0,
        (ev.text || '').slice(0, 200),
      )
    } else if (ev.kind === 'run_error') {
      // Mirror the live chat:run_errored rendering so a reopened conversation
      // shows the error (a synthetic event the backend appends for errored runs).
      const b = ensureRunBubble(ev.runId)
      b.curText = null
      const e = document.createElement('div')
      e.className = 'msg-text run-error'
      e.textContent = ev.text || ''
      b.el.appendChild(e)
      setRunStatus(ev.runId, 'errored')
    }
  }
  for (const b of runBubbles.values()) attachRunCopy(b)
  const convs = (await App.ListConversations()) || []
  const c = convs.find(x => x.id === id)
  if (c && c.pinnedPersona) {
    if (Array.from(personaSel.options).some(o => o.value === c.pinnedPersona)) {
      personaSel.value = c.pinnedPersona
    }
  }
  updateFooter()
  await loadConversations()
}

async function newChat() {
  const c = await App.CreateConversation('New conversation')
  await openConversation(c.id)
}

async function loadMeta() {
  cachedModels = (await App.Models()) || []
  cachedPersonas = (await App.Personas()) || []
  personaSel.innerHTML = cachedPersonas
    .map(p => `<option value="${p.id}">${p.name}</option>`)
    .join('')
}

async function send() {
  if (streaming || !input.value.trim()) return
  if (!activeConv) await newChat()
  // Capture the target conversation and persona before the first await. Neither
  // indexing nor the agentic run disables the sidebar or the persona picker, and
  // a run can take a minute — so any later read of activeConv/personaSel.value
  // could pick up whatever the operator clicked to next, and we would pin and
  // send against the wrong conversation.
  const conv = activeConv!
  const pid = personaSel.value
  const idxStatus = addMsg('assistant', 'Preparing textbook context…')
  ragStatusEl = idxStatus
  try {
    await App.EnsureIndexed(conv)
    idxStatus.remove()
  } catch (e: any) {
    msgText(idxStatus).textContent = `Cannot send: textbook indexing failed — ${e?.userMessage || e}`
    return
  } finally {
    ragStatusEl = null
  }
  const text = input.value.trim()
  input.value = ''
  const userEl = addMsg('user', text)
  // The assistant bubble is created by the chat:run_started event; the loop's
  // output flows in through the chat:* taxonomy below.
  streaming = true
  usagePendingForConv = conv
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    // Pin before sending: SetConversationPersona validates the persona
    // server-side and errors without writing if it can't resolve it, so
    // pinning first is safe. It also makes the pin survive a failed send,
    // which is correct — the pin records what the operator selected here,
    // not what succeeded.
    await App.SetConversationPersona(conv, pid)
    await App.SendMessage(conv, text, pid)
  } catch (e: any) {
    // A thrown error before any run started (e.g. bad model / missing key)
    // has no run bubble to attach to — surface it inline. Errors raised mid-run
    // are already rendered via chat:run_errored.
    addMsg('assistant', `[${e?.code || 'error'}] ${e?.userMessage || e}`)
    // A config rejection (typo'd mention, unknown assistant) persisted
    // nothing server-side — put the text back in the composer and drop the
    // optimistic user bubble so the view matches the store.
    if (e?.code === 'config') {
      userEl.remove()
      input.value = text
    }
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    // If the chat:usage event never arrived (cancel, SDK gap, error), mark
    // the last-known footer entry stale so the user sees a ~ marker. Only
    // do this if the operator is still looking at the conversation this
    // send targeted — usagePendingForConv is that conversation, so this
    // check also implies activeConv === conv here.
    if (usagePendingForConv === activeConv) {
      const u = latestUsage.get(conv)
      if (u) {
        latestUsage.set(conv, { ...u, stale: true })
        updateFooter()
      }
    }
    usagePendingForConv = null
    await loadConversations()
  }
}

// Assistant rendering is driven by the run-correlated chat:* taxonomy. The
// legacy chat:token (plain string) still fires for backward compatibility but
// is intentionally ignored here in favour of chat:token_v2 (carries runID).

EventsOn('chat:run_started', (p: any) => {
  if (p.convID !== activeConv) return
  ensureRunBubble(p.runID, p.personaID || '', p.modelID || '')
})

EventsOn('chat:grounding_ready', (p: any) => {
  if (p.convID !== activeConv) return
  setRunGrounding(p.runID, p.status, p.sourceCount)
})

EventsOn('chat:token_v2', (p: any) => {
  if (p.convID !== activeConv) return
  appendRunText(p.runID, p.text)
})

EventsOn('chat:tool_call', (p: any) => {
  if (p.convID !== activeConv) return
  addRunToolCall(p.runID, p.toolCallId, p.name, p.input)
})

EventsOn('chat:tool_result', (p: any) => {
  if (p.convID !== activeConv) return
  updateRunToolResult(p.runID, p.toolCallId, !!p.isError, p.errorCode || '', p.latencyMs || 0, p.summary || '')
})

EventsOn('chat:run_completed', (p: any) => {
  setRunStatus(p.runID, 'completed')
})

EventsOn('chat:run_errored', (p: any) => {
  if (p.convID === activeConv) {
    const b = ensureRunBubble(p.runID)
    b.curText = null
    const e = document.createElement('div')
    e.className = 'msg-text run-error'
    e.textContent = `[${p.errorCode || 'error'}] ${p.errorMessage || ''}`
    b.el.appendChild(e)
  }
  setRunStatus(p.runID, 'errored')
})

EventsOn('chat:run_cancelled', (p: any) => {
  setRunStatus(p.runID, 'cancelled')
})

EventsOn('chat:usage', (p: { convID: string; input: number; output: number; cached: number; lastInput: number; lastOutput: number; modelID: string }) => {
  latestUsage.set(p.convID, { ...p, stale: false })
  if (p.convID === activeConv) {
    usagePendingForConv = null
    updateFooter()
  }
})

EventsOn('rag:index', (p: any) => {
  if (ragStatusEl) msgText(ragStatusEl).textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
})

EventsOn('library:notice', (msg: string) => {
  const note = document.createElement('div')
  note.className = 'notice'
  note.textContent = '⚠ ' + msg
  // Insert before the streaming assistant bubble so chat:token still targets it.
  const lastAsst = thread.querySelector('.msg.assistant:last-child')
  if (lastAsst) thread.insertBefore(note, lastAsst)
  else thread.appendChild(note)
  thread.scrollTop = thread.scrollHeight
})

async function showTextbooks() {
  if (!activeConv) await newChat()
  const inner = $('tbModalInner')
  // Open the modal first so any backend failure renders inside it instead of
  // leaving the user with a button that "does nothing."
  inner.innerHTML = '<h3>Attach textbooks</h3>'
  $('tbModal').classList.remove('hidden')

  let books: Awaited<ReturnType<typeof App.ListBooks>>
  let current: Awaited<ReturnType<typeof App.GetConversationScope>>
  try {
    books = (await App.ListBooks()) || []
    current = (await App.GetConversationScope(activeConv!)) || []
  } catch (e: any) {
    const err = document.createElement('p')
    err.className = 'tb-error'
    err.textContent = `Could not load textbooks: ${e?.userMessage || e}`
    inner.appendChild(err)
    return
  }

  if (books.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'tb-empty'
    empty.textContent = 'No textbooks configured. Add entries to textbooks.yaml in your app directory.'
    inner.appendChild(empty)
    return
  }

  for (const b of books) {
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.dataset.book = b.name
    cb.checked = current.some(s => s.name === b.name)
    cb.disabled = !!b.error
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = b.error
      ? ` ${b.name} (unavailable: ${b.error})`
      : ` ${b.name} (${b.chapters.length} ch)`
    label.appendChild(span)
    inner.appendChild(label)
  }

  const save = document.createElement('button')
  save.textContent = 'Save'
  save.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const scopes: any[] = []
    boxes.forEach((b: any) => { if (b.checked) scopes.push({ name: b.dataset.book, chapters: null }) })
    await App.SetConversationScope(activeConv!, scopes)
    $('tbModal').classList.add('hidden')
    const banner = addMsg('assistant', 'Indexing textbooks…')
    ragStatusEl = banner
    try { await App.EnsureIndexed(activeConv!); msgText(banner).textContent = 'Textbooks ready.' }
    catch (e: any) { msgText(banner).textContent = `Indexing failed: ${e?.userMessage || e}` }
    finally { ragStatusEl = null }
  }
  inner.appendChild(save)
}

// ---- Prompt / context library ----------------------------------------------

const libModal = $('libModal')
const editorView = $('editorView')
const editorArea = $('editorArea') as HTMLTextAreaElement
const editorTitle = $('editorTitle')
const editorMsg = $('editorMsg')
const editorDelete = $('editorDelete') as HTMLButtonElement

let editingFile: string | null = null // null = creating a new item

async function openLibraryPanel() {
  if (!activeConv) await newChat()
  const items = (await App.ListLibraryItems()) || []
  const active = new Set((await App.GetActiveItems(activeConv!)) || [])
  const inner = $('libModalInner')
  inner.innerHTML = '<h3>Prompt / context library</h3>'
  if (items.length === 0) {
    inner.innerHTML += '<p class="lib-empty">No items yet. Create one to get started.</p>'
  }
  for (const it of items) {
    const row = document.createElement('div')
    row.className = 'lib-row'
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.checked = active.has(it.filename)
    cb.disabled = !!it.error
    cb.dataset.file = it.filename
    cb.onchange = saveActive
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = it.error ? `${it.name} (unreadable)` : it.name
    label.appendChild(span)
    row.appendChild(label)
    const edit = document.createElement('button')
    edit.className = 'lib-edit'
    edit.textContent = 'Edit'
    edit.onclick = () => void openEditor(it.filename)
    row.appendChild(edit)
    inner.appendChild(row)
  }
  const add = document.createElement('button')
  add.className = 'lib-new'
  add.textContent = '+ New item'
  add.onclick = () => void openEditor(null)
  inner.appendChild(add)
  libModal.classList.remove('hidden')
}

async function saveActive() {
  const boxes = $('libModalInner').querySelectorAll('input[type=checkbox]')
  const names: string[] = []
  boxes.forEach((b) => {
    const i = b as HTMLInputElement
    if (i.checked && i.dataset.file) names.push(i.dataset.file)
  })
  await App.SetActiveItems(activeConv!, names)
}

async function openEditor(file: string | null) {
  editingFile = file
  editorMsg.textContent = ''
  if (file) {
    editorTitle.textContent = 'Edit item'
    editorDelete.classList.remove('hidden')
    try {
      editorArea.value = await App.ReadLibraryItem(file)
    } catch (e: any) {
      editorArea.value = ''
      editorMsg.textContent = e?.userMessage || String(e)
    }
  } else {
    editorTitle.textContent = 'New item'
    editorDelete.classList.add('hidden')
    editorArea.value = ''
  }
  libModal.classList.add('hidden')
  editorView.classList.remove('hidden')
  editorArea.focus()
}

function closeEditor() {
  editorView.classList.add('hidden')
}

async function saveEditor() {
  const content = editorArea.value
  try {
    if (editingFile) {
      await App.SaveLibraryItem(editingFile, content)
    } else {
      await App.CreateLibraryItem(content)
    }
  } catch (e: any) {
    editorMsg.textContent = e?.userMessage || String(e)
    return
  }
  closeEditor()
  await openLibraryPanel()
}

async function deleteEditorItem() {
  if (!editingFile) return
  try {
    await App.DeleteLibraryItem(editingFile)
  } catch (e: any) {
    editorMsg.textContent = e?.userMessage || String(e)
    return
  }
  closeEditor()
  await openLibraryPanel()
}

initPipeline()
void refreshReviewsDue() // launch sweep: badge appears if anything is due

$('newChat').onclick = newChat
sendBtn.onclick = () => { if (streaming) { App.CancelMessage() } else { void send() } }
$('tbBtn').onclick = showTextbooks
$('tbModal').onclick = (e) => { if (e.target === $('tbModal')) $('tbModal').classList.add('hidden') }
$('libBtn').onclick = () => void openLibraryPanel()
libModal.onclick = (e) => { if (e.target === libModal) libModal.classList.add('hidden') }
$('editorBack').onclick = () => { closeEditor(); void openLibraryPanel() }
$('editorSave').onclick = () => void saveEditor()
editorDelete.onclick = () => void deleteEditorItem()
// --- @mention autocomplete -------------------------------------------
// Mentions are leading-only, so the popup exists only while the composer
// holds nothing but a partial leading @name. Pasting code mid-message can
// never trigger it — that is the entire reason for the leading-only rule.
let mentionMatches: typeof cachedPersonas = []
let mentionSel = 0
let mentionDismissed = false

function mentionPrefix(): string | null {
  const m = /^\s*@([a-zA-Z0-9-]*)$/.exec(input.value)
  return m ? m[1].toLowerCase() : null
}

function hideMentionPopup() {
  mentionPopup.classList.add('hidden')
  mentionMatches = []
}

function updateMentionPopup() {
  const prefix = mentionPrefix()
  if (prefix === null) { mentionDismissed = false; hideMentionPopup(); return }
  if (mentionDismissed) { hideMentionPopup(); return }
  mentionMatches = cachedPersonas.filter(p => p.id.startsWith(prefix))
  if (!mentionMatches.length) { hideMentionPopup(); return }
  if (mentionSel >= mentionMatches.length) mentionSel = 0
  mentionPopup.innerHTML = mentionMatches.map((p, i) =>
    `<div class="mention-item${i === mentionSel ? ' sel' : ''}" data-id="${p.id}">` +
    `<span class="mention-dot" style="background:${p.color}"></span>` +
    `<span>${p.name}</span><span class="mention-id">@${p.id}</span></div>`
  ).join('')
  mentionPopup.classList.remove('hidden')
}

function insertMention(id: string) {
  input.value = '@' + id + ' '
  hideMentionPopup()
  input.focus()
  input.setSelectionRange(input.value.length, input.value.length)
}

input.addEventListener('input', () => { mentionSel = 0; updateMentionPopup() })
// mousedown, not click: it fires before the textarea loses focus.
mentionPopup.addEventListener('mousedown', (e) => {
  const item = (e.target as HTMLElement).closest('.mention-item') as HTMLElement | null
  if (item?.dataset.id) { e.preventDefault(); insertMention(item.dataset.id) }
})

input.addEventListener('keydown', (e) => {
  if (!mentionPopup.classList.contains('hidden')) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      mentionSel = (mentionSel + 1) % mentionMatches.length
      updateMentionPopup()
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      mentionSel = (mentionSel + mentionMatches.length - 1) % mentionMatches.length
      updateMentionPopup()
      return
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      insertMention(mentionMatches[mentionSel].id)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      mentionDismissed = true
      hideMentionPopup()
      return
    }
  }
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
})

;(async () => {
  const issues = (await App.StartupIssues()) || []
  if (issues.length) addMsg('assistant', '⚠ Setup:\n' + issues.join('\n'))
  await loadMeta()
  await loadConversations()
})()
