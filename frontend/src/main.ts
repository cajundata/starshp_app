import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { EventsOn } from '../wailsjs/runtime/runtime'

let activeConv: string | null = null
let streaming = false

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
  const convs = (await App.ListConversations()) || []
  const c = convs.find(x => x.id === id)
  if (c && c.pinnedModel) {
    if (Array.from(modelSel.options).some(o => o.value === c.pinnedModel)) {
      modelSel.value = c.pinnedModel
    }
  }
  await loadConversations()
}

async function newChat() {
  const c = await App.CreateConversation('New conversation')
  await openConversation(c.id)
}

async function loadMeta() {
  const models = (await App.Models()) || []
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
    await loadConversations()
  }
}

EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child .msg-text')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
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
  const books = (await App.ListBooks()) || []
  const current = (await App.GetConversationScope(activeConv!)) || []
  const inner = $('tbModalInner')
  inner.innerHTML = '<h3>Attach textbooks</h3>'
  for (const b of books) {
    const checked = current.some(s => s.name === b.name) ? 'checked' : ''
    inner.innerHTML += `<label><input type="checkbox" data-book="${b.name}" ${checked}/> ${b.name} (${b.chapters.length} ch)</label>`
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
  $('tbModal').classList.remove('hidden')
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
