import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { store } from '../wailsjs/go/models'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { initPipeline, refreshReviewsDue } from './pipeline'

let activeConv: string | null = null
let streaming = false

type Usage = { input: number; output: number; cached: number; modelID: string; stale: boolean }
const latestUsage = new Map<string, Usage>()
let usagePendingForConv: string | null = null  // set at send-start; cleared by chat:usage; if still set after send completes, mark stale

const fmt = (n: number) => n.toLocaleString('en-US')

function modelMaxContext(modelID: string, models: { id: string; maxContext?: number }[]): number {
  const m = models.find(x => x.id === modelID)
  return m?.maxContext ?? 0
}

let cachedModels: { id: string; maxContext?: number }[] = []

function updateFooter() {
  const el = $('ctxFooter')
  if (!activeConv) { el.classList.add('hidden'); el.textContent = ''; return }
  const u = latestUsage.get(activeConv)
  if (!u) { el.classList.add('hidden'); el.textContent = ''; return }
  const max = modelMaxContext(u.modelID, cachedModels)
  const prefix = u.stale ? '~' : ''
  const denom = max > 0 ? ` / ${fmt(max)}` : ''
  el.textContent = `ctx ${prefix}${fmt(u.input)}${denom} · cache ${fmt(u.cached)}`
  el.classList.remove('hidden')
}

const $ = (id: string) => document.getElementById(id) as HTMLElement
const thread = $('thread')
const input = $('input') as HTMLTextAreaElement
const modelSel = $('modelSel') as HTMLSelectElement
const sendBtn = $('sendBtn') as HTMLButtonElement

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

function ensureRunBubble(runId: string): RunBubble {
  let b = runBubbles.get(runId)
  if (!b) {
    const el = document.createElement('div')
    el.className = 'msg assistant'
    thread.appendChild(el)
    thread.scrollTop = thread.scrollHeight
    b = { el, curText: null, tools: new Map() }
    runBubbles.set(runId, b)
  }
  return b
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
  b.el.insertBefore(h, b.el.firstChild)
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
  // user saw is preserved). Token usage is not carried on events, so the footer
  // stays empty until the next live turn emits chat:usage.
  const events = (await App.GetConversationDisplayEvents(id)) || []
  for (const ev of events) {
    if (ev.kind === 'user_message') {
      addMsg('user', ev.text || '')
      continue
    }
    if (!ev.runId) continue
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
  if (c && c.pinnedModel) {
    if (Array.from(modelSel.options).some(o => o.value === c.pinnedModel)) {
      modelSel.value = c.pinnedModel
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
  const models = (await App.Models()) || []
  cachedModels = models
  modelSel.innerHTML = models.map(m => `<option value="${m.id}">${m.display}</option>`).join('')
}

async function send() {
  if (streaming || !input.value.trim()) return
  if (!activeConv) await newChat()
  const idxStatus = addMsg('assistant', 'Preparing textbook context…')
  ragStatusEl = idxStatus
  try {
    await App.EnsureIndexed(activeConv!)
    idxStatus.remove()
  } catch (e: any) {
    msgText(idxStatus).textContent = `Cannot send: textbook indexing failed — ${e?.userMessage || e}`
    return
  } finally {
    ragStatusEl = null
  }
  const text = input.value.trim()
  input.value = ''
  addMsg('user', text)
  // The assistant bubble is created by the chat:run_started event; the loop's
  // output flows in through the chat:* taxonomy below.
  streaming = true
  usagePendingForConv = activeConv
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    await App.SendMessage(activeConv!, text, modelSel.value)
    await App.SetConversationMeta(activeConv!, modelSel.value)
  } catch (e: any) {
    // A thrown error before any run started (e.g. bad model / missing key)
    // has no run bubble to attach to — surface it inline. Errors raised mid-run
    // are already rendered via chat:run_errored.
    addMsg('assistant', `[${e?.code || 'error'}] ${e?.userMessage || e}`)
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    // If the chat:usage event never arrived (cancel, SDK gap, error), mark
    // the last-known footer entry stale so the user sees a ~ marker.
    if (usagePendingForConv === activeConv) {
      const u = latestUsage.get(activeConv!)
      if (u) {
        latestUsage.set(activeConv!, { ...u, stale: true })
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
  ensureRunBubble(p.runID)
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

EventsOn('chat:usage', (p: { convID: string; input: number; output: number; cached: number; modelID: string }) => {
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

// pickTextbooks opens the textbook modal as a reusable picker. It lists books,
// pre-checks `current`, and on confirm calls onConfirm(selected) — closing the
// modal on success, or showing the error inline on failure.
async function pickTextbooks(
  current: any[],
  confirmLabel: string,
  onConfirm: (scopes: any[]) => Promise<void>,
) {
  const inner = $('tbModalInner')
  inner.innerHTML = '<h3>Attach textbooks</h3>'
  $('tbModal').classList.remove('hidden')

  let books: Awaited<ReturnType<typeof App.ListBooks>>
  try {
    books = (await App.ListBooks()) || []
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
  }

  for (const b of books) {
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.dataset.book = b.name
    cb.checked = current.some((s: any) => s.name === b.name)
    cb.disabled = !!b.error
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = b.error
      ? ` ${b.name} (unavailable: ${b.error})`
      : ` ${b.name} (${b.chapters.length} ch)`
    label.appendChild(span)
    inner.appendChild(label)
  }

  const status = document.createElement('p')
  status.className = 'tb-empty'
  inner.appendChild(status)

  const confirm = document.createElement('button')
  confirm.textContent = confirmLabel
  confirm.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const available = new Set(books.map(b => b.name))
    const scopes: any[] = []
    boxes.forEach((b: any) => { if (b.checked) scopes.push({ name: b.dataset.book, chapters: null }) })
    // Preserve already-attached books that aren't in the current catalog, so a
    // transient ListBooks gap can't silently wipe a stored scope.
    for (const s of current) {
      if (!available.has(s.name)) scopes.push({ name: s.name, chapters: null })
    }
    confirm.disabled = true
    status.className = 'tb-empty'
    status.textContent = scopes.length ? 'Indexing textbooks…' : 'Working…'
    try {
      await onConfirm(scopes)
      $('tbModal').classList.add('hidden')
    } catch (e: any) {
      status.className = 'tb-error'
      status.textContent = `Failed: ${e?.userMessage || e}`
      confirm.disabled = false
    }
  }
  inner.appendChild(confirm)
}

// openAssignmentTextbookEditor edits the current assignment's textbook scope.
// cf. pickTextbooks / showTextbooks (shared #tbModal, different confirm semantics).
async function openAssignmentTextbookEditor() {
  const id = currentAssignmentId
  if (!id) return
  let current: any[]
  try {
    current = (await App.GetAssignmentScope(id)) || []
  } catch (e) {
    // Don't open with empty state — a Save would wipe the real (unloaded) scope.
    console.warn('GetAssignmentScope failed; not opening textbook editor', e)
    return
  }
  await pickTextbooks(current, 'Save', async (scopes) => {
    await App.EnsureIndexedScope(scopes)
    await App.SetAssignmentScope(id, scopes)
  })
}

// pickLibraryItems opens the library modal as a reusable picker. It lists items,
// pre-checks `current` (by filename), and on confirm calls onConfirm(selected
// filenames) — closing the modal on success, or showing the error inline.
async function pickLibraryItems(
  current: string[],
  confirmLabel: string,
  onConfirm: (items: string[]) => Promise<void>,
) {
  const inner = $('libModalInner')
  inner.innerHTML = '<h3>Prompt / context library</h3>'
  $('libModal').classList.remove('hidden')

  let items: Awaited<ReturnType<typeof App.ListLibraryItems>>
  try {
    items = (await App.ListLibraryItems()) || []
  } catch (e: any) {
    const err = document.createElement('p')
    err.className = 'lib-error'
    err.textContent = `Could not load library: ${e?.userMessage || e}`
    inner.appendChild(err)
    return
  }

  if (items.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'lib-empty'
    empty.textContent = 'No library items yet. Create one in the Library panel.'
    inner.appendChild(empty)
  }

  for (const it of items) {
    const row = document.createElement('div')
    row.className = 'lib-row'
    const label = document.createElement('label')
    const cb = document.createElement('input')
    cb.type = 'checkbox'
    cb.dataset.file = it.filename
    cb.checked = current.includes(it.filename)
    cb.disabled = !!it.error
    label.appendChild(cb)
    const span = document.createElement('span')
    span.textContent = it.error ? ` ${it.name} (unavailable)` : ` ${it.name}`
    label.appendChild(span)
    row.appendChild(label)
    inner.appendChild(row)
  }

  const status = document.createElement('p')
  status.className = 'lib-empty'
  inner.appendChild(status)

  const confirm = document.createElement('button')
  confirm.className = 'lib-new'
  confirm.textContent = confirmLabel
  confirm.onclick = async () => {
    const boxes = inner.querySelectorAll('input[type=checkbox]')
    const sel: string[] = []
    boxes.forEach((b: any) => { if (b.checked && b.dataset.file) sel.push(b.dataset.file) })
    confirm.disabled = true
    status.className = 'lib-empty'
    status.textContent = 'Working…'
    try {
      await onConfirm(sel)
      $('libModal').classList.add('hidden')
    } catch (e: any) {
      status.className = 'lib-error'
      status.textContent = `Failed: ${e?.userMessage || e}`
      confirm.disabled = false
    }
  }
  inner.appendChild(confirm)
}

// openAssignmentLibraryEditor edits the current assignment's library selection.
async function openAssignmentLibraryEditor() {
  const id = currentAssignmentId
  if (!id) return
  let current: string[]
  try {
    current = (await App.GetAssignmentLibraryItems(id)) || []
  } catch (e) {
    // Don't open with empty state — a Save would wipe the real (unloaded) selection.
    console.warn('GetAssignmentLibraryItems failed; not opening prompt editor', e)
    return
  }
  await pickLibraryItems(current, 'Save', async (items) => {
    await App.SetAssignmentLibraryItems(id, items)
  })
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

// ---- Assignments review view -----------------------------------------------
// A batch "assignment" solves a folder of questions. The view lists items with
// status/confidence/flag indicators; live progress arrives via assignment:*
// events. Drilling into an item renders its persisted run (display events) with
// the same bubble/tool-block structure the chat thread uses.

const asgView = $('asgView')
const asgHeader = $('asgHeader')
const asgItems = $('asgItems')
const asgDetail = $('asgDetail')
const asgStopBtn = $('asgStopBtn') as HTMLButtonElement

let currentAssignmentId: string | null = null
let selectedItem: store.AssignmentItem | null = null
let currentAssignmentStatus = ''
const RERUNNABLE_STATUSES = ['answered', 'no_answer', 'errored', 'cancelled']
// Index item rows by seq so progress events can update them in place.
const asgItemRows = new Map<number, HTMLElement>()
let asgProgressDone = 0
let asgProgressTotal = 0

function openAssignments() {
  asgView.classList.remove('hidden')
  void loadAssignmentsHome()
}

function closeAssignments() {
  asgView.classList.add('hidden')
}

// loadAssignmentsHome shows the most recent assignment (if any) so the view is
// not blank on open; the explicit "Solve a folder…" action starts a new batch.
async function loadAssignmentsHome() {
  let list: Awaited<ReturnType<typeof App.ListAssignments>>
  try {
    list = (await App.ListAssignments()) || []
  } catch (e: any) {
    asgHeader.innerHTML = ''
    const err = document.createElement('p')
    err.className = 'asg-error'
    err.textContent = `Could not load assignments: ${e?.userMessage || e}`
    asgHeader.appendChild(err)
    return
  }
  if (currentAssignmentId) return // a batch is already loaded/in-flight
  if (list.length === 0) {
    asgHeader.innerHTML = '<p class="asg-empty">No assignments yet. Solve a folder to get started.</p>'
    asgItems.innerHTML = ''
    asgDetail.innerHTML = ''
    return
  }
  await selectAssignment(list[0].ID)
}

async function solveFolder() {
  const dir = prompt('Folder to solve (absolute path):')
  if (!dir || !dir.trim()) return
  const d = dir.trim()
  // Pre-fill the pickers from the most recent assignment for this folder so a
  // re-solve doesn't default to empty (and wipe a stored selection). Best-effort.
  let preScopes: any[] = []
  let preItems: string[] = []
  try {
    [preScopes, preItems] = await Promise.all([
      App.GetAssignmentScopeForDir(d).then(r => r || []),
      App.GetAssignmentLibraryItemsForDir(d).then(r => r || []),
    ])
  } catch (e) {
    // Best-effort pre-fill: default to empty, but surface unexpected failures.
    console.debug('solve pre-fill fetch failed; defaulting to empty', e)
  }
  await pickTextbooks(preScopes, 'Next: Prompts →', async (scopes) => {
    await pickLibraryItems(preItems, 'Solve', async (items) => {
      asgDetail.innerHTML = ''
      asgHeader.innerHTML = '<p class="asg-empty">Preparing…</p>'
      try {
        await App.EnsureIndexedScope(scopes)
        const id = await App.SolveAssignment(d, scopes, items)
        currentAssignmentId = id
        asgItemRows.clear()
        asgStopBtn.classList.remove('hidden')
        await selectAssignment(id)
      } catch (e) {
        asgHeader.innerHTML = ''
        throw e
      }
    })
  })
}

// selectAssignment loads an assignment's header + items from the store. Used on
// open, after solve start, and on completed/cancelled refresh.
async function selectAssignment(id: string): Promise<store.AssignmentItem[]> {
  currentAssignmentId = id
  asgItemRows.clear()
  selectedItem = null
  asgDetail.innerHTML = ''
  let asg: Awaited<ReturnType<typeof App.GetAssignment>>
  let items: Awaited<ReturnType<typeof App.ListAssignmentItems>>
  try {
    asg = await App.GetAssignment(id)
    items = (await App.ListAssignmentItems(id)) || []
    currentAssignmentStatus = asg.Status || ''
  } catch (e: any) {
    asgHeader.innerHTML = ''
    const err = document.createElement('p')
    err.className = 'asg-error'
    err.textContent = `Could not load assignment: ${e?.userMessage || e}`
    asgHeader.appendChild(err)
    return []
  }
  const done = items.filter(it => it.Status !== 'pending' && it.Status !== 'solving').length
  renderAssignmentHeader(asg.Title || asg.SourceDir, done, asg.TotalItems || items.length, asg.Status)
  asgItems.innerHTML = ''
  for (const it of items) renderItemRow(it)
  // A still-running batch keeps the Stop button visible.
  if (asg.Status === 'in_progress') asgStopBtn.classList.remove('hidden')
  else asgStopBtn.classList.add('hidden')
  return items
}

function renderAssignmentHeader(title: string, done: number, total: number, status: string) {
  asgProgressDone = done
  asgProgressTotal = total
  asgHeader.innerHTML = ''
  const h = document.createElement('div')
  h.className = 'asg-title'
  h.textContent = title
  asgHeader.appendChild(h)

  const sub = document.createElement('div')
  sub.className = 'asg-sub'
  const pill = document.createElement('span')
  pill.className = 'status-pill status-' + (status || 'unknown')
  pill.textContent = status || 'unknown'
  sub.appendChild(pill)
  const tbBtn = document.createElement('button')
  tbBtn.className = 'asg-tb-btn'
  tbBtn.textContent = '📚 Textbooks'
  tbBtn.onclick = () => void openAssignmentTextbookEditor()
  sub.appendChild(tbBtn)
  const libBtn = document.createElement('button')
  libBtn.className = 'asg-tb-btn'
  libBtn.textContent = '📝 Prompts'
  libBtn.onclick = () => void openAssignmentLibraryEditor()
  sub.appendChild(libBtn)
  asgHeader.appendChild(sub)

  const bar = document.createElement('div')
  bar.className = 'assignment-progress'
  const fill = document.createElement('div')
  fill.className = 'assignment-progress-fill'
  fill.style.width = total > 0 ? `${Math.round((done / total) * 100)}%` : '0%'
  bar.appendChild(fill)
  asgHeader.appendChild(bar)

  const count = document.createElement('div')
  count.className = 'assignment-progress-count'
  count.textContent = `${done} / ${total}`
  asgHeader.appendChild(count)
}

function updateProgress(doneDelta: number) {
  asgProgressDone += doneDelta
  const fill = asgHeader.querySelector('.assignment-progress-fill') as HTMLElement | null
  if (fill) fill.style.width = asgProgressTotal > 0 ? `${Math.round((asgProgressDone / asgProgressTotal) * 100)}%` : '0%'
  const count = asgHeader.querySelector('.assignment-progress-count') as HTMLElement | null
  if (count) count.textContent = `${asgProgressDone} / ${asgProgressTotal}`
}

function confidenceClass(c: string): string {
  if (c === 'high' || c === 'medium' || c === 'low') return 'confidence-' + c
  return 'confidence-unknown'
}

function renderItemRow(it: store.AssignmentItem) {
  const flagCount = flagCountFromJSON(it.FlagsJSON)
  const row = document.createElement('div')
  row.className = 'assignment-item'
  row.dataset.seq = String(it.Seq)
  applyItemDecorations(row, it.Confidence, flagCount)

  const seq = document.createElement('span')
  seq.className = 'item-seq'
  seq.textContent = String(it.Seq)
  row.appendChild(seq)

  const title = document.createElement('span')
  title.className = 'item-title'
  title.textContent = it.Title || it.SourcePath || '(untitled)'
  row.appendChild(title)

  const type = document.createElement('span')
  type.className = 'item-type'
  type.textContent = it.Type || ''
  row.appendChild(type)

  const conf = document.createElement('span')
  conf.className = 'item-conf ' + confidenceClass(it.Confidence)
  conf.textContent = it.Confidence || '—'
  row.appendChild(conf)

  const flag = document.createElement('span')
  flag.className = 'item-flag'
  flag.textContent = flagCount > 0 ? `⚑ ${flagCount}` : ''
  row.appendChild(flag)

  const pill = document.createElement('span')
  pill.className = 'status-pill status-' + (it.Status || 'pending')
  pill.textContent = it.Status || 'pending'
  row.appendChild(pill)

  // Only items with a persisted conversation can be drilled into.
  if (it.ConversationID) {
    row.classList.add('drillable')
    row.onclick = () => {
      selectedItem = it
      void openItemDetail(it.ConversationID, it.Seq)
    }
  }

  asgItems.appendChild(row)
  asgItemRows.set(it.Seq, row)
}

function applyItemDecorations(row: HTMLElement, confidence: string, flagCount: number) {
  row.classList.toggle('item-flagged', flagCount > 0)
  row.classList.toggle('item-low', confidence === 'low')
}

function flagCountFromJSON(s: string): number {
  if (!s) return 0
  try {
    const arr = JSON.parse(s)
    return Array.isArray(arr) ? arr.length : 0
  } catch {
    return 0
  }
}

// ---- Item detail: render a persisted run from display events ---------------
// Mirrors the chat thread's bubble/tool-block structure (same CSS) but targets
// the assignment detail container instead of the global thread.

// toolInput crosses the wails bridge as a parsed JSON value (Go json.RawMessage
// marshals as raw JSON), so it's normally an object — not a string or byte array.
// Older rows / other shapes may still be a string or byte array; handle all three.
function toolInputText(v: any): string {
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (v instanceof Uint8Array) {
    try { return new TextDecoder().decode(v) } catch { return '' }
  }
  try { return JSON.stringify(v, null, 2) } catch { return '' }
}

async function openItemDetail(conversationId: string, seq: number) {
  asgItems.querySelectorAll('.assignment-item.selected').forEach(n => n.classList.remove('selected'))
  asgItemRows.get(seq)?.classList.add('selected')
  asgDetail.innerHTML = ''
  renderDetailHeader()
  let events: Awaited<ReturnType<typeof App.GetConversationDisplayEvents>>
  try {
    events = (await App.GetConversationDisplayEvents(conversationId)) || []
  } catch (e: any) {
    const err = document.createElement('p')
    err.className = 'asg-error'
    err.textContent = `Could not load run: ${e?.userMessage || e}`
    asgDetail.appendChild(err)
    return
  }
  // Build run bubbles keyed by runId inside the detail container.
  const bubbles = new Map<string, { el: HTMLElement; curText: HTMLElement | null; tools: Map<string, HTMLElement> }>()
  const ensure = (runId: string) => {
    let b = bubbles.get(runId)
    if (!b) {
      const el = document.createElement('div')
      el.className = 'msg assistant'
      asgDetail.appendChild(el)
      b = { el, curText: null, tools: new Map() }
      bubbles.set(runId, b)
    }
    return b
  }
  for (const ev of events) {
    if (ev.kind === 'user_message') {
      const um = document.createElement('div')
      um.className = 'msg user'
      const t = document.createElement('div')
      t.className = 'msg-text'
      t.textContent = ev.text || ''
      um.appendChild(t)
      asgDetail.appendChild(um)
      continue
    }
    if (!ev.runId) continue
    const b = ensure(ev.runId)
    if (ev.kind === 'assistant_text') {
      if (!b.curText) {
        b.curText = document.createElement('div')
        b.curText.className = 'msg-text'
        b.el.appendChild(b.curText)
      }
      b.curText.textContent += ev.text || ''
    } else if (ev.kind === 'assistant_tool_call') {
      b.curText = null
      const div = document.createElement('div')
      div.className = 'tool-call'
      const icon = document.createElement('span')
      icon.className = 'tool-icon'
      icon.textContent = toolIcon(ev.toolName || '')
      const nm = document.createElement('span')
      nm.className = 'tool-name'
      nm.textContent = ev.toolName || ''
      const arg = document.createElement('span')
      arg.className = 'tool-arg'
      arg.textContent = argPreview(ev.toolInput)
      div.append(icon, nm, arg)
      const full = toolInputText(ev.toolInput)
      if (full) {
        const detail = document.createElement('div')
        detail.className = 'tool-summary'
        detail.textContent = full
        div.appendChild(detail)
      }
      b.el.appendChild(div)
      b.tools.set(ev.toolCallId || '', div)
    } else if (ev.kind === 'tool_result') {
      const el = b.tools.get(ev.toolCallId || '')
      const txt = ev.text || ''
      if (el && txt) {
        const detail = document.createElement('div')
        detail.className = 'tool-summary'
        detail.textContent = txt
        if (ev.isError) el.classList.add('errored')
        el.appendChild(detail)
      }
    }
  }
  if (bubbles.size === 0) {
    const empty = document.createElement('p')
    empty.className = 'asg-empty'
    empty.textContent = 'No worked run recorded for this item.'
    asgDetail.appendChild(empty)
  }
}

function itemRerunnable(it: store.AssignmentItem | null): boolean {
  return !!it
    && it.Type !== 'unsupported'
    && RERUNNABLE_STATUSES.includes(it.Status)
    && currentAssignmentStatus !== 'in_progress'
}

function renderDetailHeader() {
  const header = document.createElement('div')
  header.className = 'asg-detail-header'
  if (itemRerunnable(selectedItem)) {
    const btn = document.createElement('button')
    btn.className = 'asg-rerun-btn'
    btn.textContent = '↻ Rerun'
    btn.onclick = () => void rerunSelectedItem(btn)
    header.appendChild(btn)
  }
  const msg = document.createElement('span')
  msg.className = 'asg-rerun-msg'
  header.appendChild(msg)
  asgDetail.appendChild(header)
}

async function rerunSelectedItem(btn: HTMLButtonElement) {
  if (!selectedItem || !currentAssignmentId) return
  const seq = selectedItem.Seq
  const prior = selectedItem
  const msg = asgDetail.querySelector('.asg-rerun-msg') as HTMLElement | null
  const prevLabel = btn.textContent
  btn.disabled = true
  btn.textContent = '↻ Rerunning…'
  if (msg) msg.textContent = ''
  const pill = asgItemRows.get(seq)?.querySelector('.status-pill') as HTMLElement | null
  if (pill) {
    pill.className = 'status-pill status-solving'
    pill.textContent = 'solving'
  }
  try {
    await App.RerunAssignmentItem(currentAssignmentId, seq)
    // selectAssignment refetches + rebuilds the rows; reuse its items (no 2nd round-trip).
    const items = await selectAssignment(currentAssignmentId)
    const fresh = items.find(i => i.Seq === seq) || null
    selectedItem = fresh
    // On success we intentionally leave this button disabled: openItemDetail below
    // rebuilds the detail header from scratch (re-evaluating itemRerunnable).
    if (fresh && fresh.ConversationID) {
      await openItemDetail(fresh.ConversationID, seq)
    }
  } catch (e: any) {
    if (pill) {
      pill.className = 'status-pill status-' + prior.Status
      pill.textContent = prior.Status
    }
    btn.disabled = false
    btn.textContent = prevLabel || '↻ Rerun'
    if (msg) msg.textContent = e?.userMessage || String(e)
  }
}

// ---- Live progress events --------------------------------------------------

EventsOn('assignment:started', (p: any) => {
  if (p.assignmentId !== currentAssignmentId) return
  asgItemRows.clear()
  asgItems.innerHTML = ''
  renderAssignmentHeader(p.title || '', 0, p.total || 0, 'running')
  asgStopBtn.classList.remove('hidden')
})

EventsOn('assignment:item_started', (p: any) => {
  if (p.assignmentId !== currentAssignmentId) return
  let row = asgItemRows.get(p.seq)
  if (!row) {
    // Row not yet rendered (fresh batch) — create a placeholder.
    row = document.createElement('div')
    row.className = 'assignment-item'
    row.dataset.seq = String(p.seq)
    const seq = document.createElement('span'); seq.className = 'item-seq'; seq.textContent = String(p.seq)
    const title = document.createElement('span'); title.className = 'item-title'; title.textContent = p.title || ''
    const type = document.createElement('span'); type.className = 'item-type'; type.textContent = p.type || ''
    const conf = document.createElement('span'); conf.className = 'item-conf confidence-unknown'; conf.textContent = '—'
    const flag = document.createElement('span'); flag.className = 'item-flag'
    const pill = document.createElement('span'); pill.className = 'status-pill status-solving'; pill.textContent = 'solving'
    row.append(seq, title, type, conf, flag, pill)
    asgItems.appendChild(row)
    asgItemRows.set(p.seq, row)
  } else {
    const pill = row.querySelector('.status-pill') as HTMLElement
    if (pill) { pill.className = 'status-pill status-solving'; pill.textContent = 'solving' }
  }
})

EventsOn('assignment:item_done', (p: any) => {
  if (p.assignmentId !== currentAssignmentId) return
  const row = asgItemRows.get(p.seq)
  if (row) {
    const pill = row.querySelector('.status-pill') as HTMLElement
    if (pill) { pill.className = 'status-pill status-' + (p.status || 'done'); pill.textContent = p.status || 'done' }
    const conf = row.querySelector('.item-conf') as HTMLElement
    if (conf) { conf.className = 'item-conf ' + confidenceClass(p.confidence); conf.textContent = p.confidence || '—' }
    const flag = row.querySelector('.item-flag') as HTMLElement
    if (flag) flag.textContent = p.flagCount > 0 ? `⚑ ${p.flagCount}` : ''
    applyItemDecorations(row, p.confidence, p.flagCount || 0)
  }
  updateProgress(1)
})

EventsOn('assignment:completed', (p: any) => {
  if (p.assignmentId !== currentAssignmentId) return
  asgStopBtn.classList.add('hidden')
  if (currentAssignmentId) void selectAssignment(currentAssignmentId)
})

EventsOn('assignment:cancelled', (p: any) => {
  if (p.assignmentId !== currentAssignmentId) return
  asgStopBtn.classList.add('hidden')
  if (currentAssignmentId) void selectAssignment(currentAssignmentId)
})

$('asgBtn').onclick = () => openAssignments()
$('asgBack').onclick = () => closeAssignments()
$('asgSolveBtn').onclick = () => void solveFolder()
asgStopBtn.onclick = () => {
  if (currentAssignmentId) void App.CancelAssignment(currentAssignmentId)
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
input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
})

;(async () => {
  const issues = (await App.StartupIssues()) || []
  if (issues.length) addMsg('assistant', '⚠ Setup:\n' + issues.join('\n'))
  await loadMeta()
  await loadConversations()
})()
