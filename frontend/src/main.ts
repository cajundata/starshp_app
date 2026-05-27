import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { EventsOn } from '../wailsjs/runtime/runtime'

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

function attachCopyButton(msgEl: HTMLElement) {
  if (msgEl.querySelector('.msg-actions')) return
  const row = document.createElement('div')
  row.className = 'msg-actions'
  const btn = document.createElement('button')
  btn.className = 'copy-btn'
  btn.title = 'Copy'
  btn.innerHTML = COPY_ICON
  let revertTimer: ReturnType<typeof setTimeout> | null = null
  btn.onclick = async () => {
    try {
      await navigator.clipboard.writeText(msgText(msgEl).textContent || '')
      if (revertTimer !== null) clearTimeout(revertTimer)
      btn.classList.add('copied')
      btn.innerHTML = CHECK_ICON
      revertTimer = setTimeout(() => {
        btn.classList.remove('copied')
        btn.innerHTML = COPY_ICON
        revertTimer = null
      }, 1500)
    } catch {
      // clipboard unavailable — leave the icon unchanged, no crash
    }
  }
  row.appendChild(btn)
  msgEl.appendChild(row)
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
  const msgs = (await App.ListMessages(id)) || []
  for (const m of msgs) {
    const el = addMsg(m.role, m.content)
    if (m.role === 'assistant' && m.content.trim()) attachCopyButton(el)
  }
  // Seed footer from the most recent assistant message that carries usage.
  if (!latestUsage.has(id)) {
    for (let i = msgs.length - 1; i >= 0; i--) {
      const m = msgs[i] as any
      if (m.role === 'assistant' && m.inputTokens != null && m.outputTokens != null) {
        latestUsage.set(id, {
          input: m.inputTokens,
          output: m.outputTokens,
          cached: m.cachedInputTokens ?? 0,
          modelID: m.model || '',
          stale: false,
        })
        break
      }
    }
  }
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
  const asst = addMsg('assistant', '')
  streaming = true
  usagePendingForConv = activeConv
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    await App.SendMessage(activeConv!, text, modelSel.value)
    await App.SetConversationMeta(activeConv!, modelSel.value)
  } catch (e: any) {
    msgText(asst).textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    if (msgText(asst).textContent?.trim()) attachCopyButton(asst)
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

EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child .msg-text')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
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
