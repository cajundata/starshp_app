import './style.css'
import * as App from '../wailsjs/go/appapi/API'
import { EventsOn } from '../wailsjs/runtime/runtime'

let activeConv: string | null = null
let streaming = false

const $ = (id: string) => document.getElementById(id) as HTMLElement
const thread = $('thread')
const input = $('input') as HTMLTextAreaElement
const modelSel = $('modelSel') as HTMLSelectElement
const presetSel = $('presetSel') as HTMLSelectElement
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
    d.textContent = c.title
    d.onclick = () => openConversation(c.id)
    list.appendChild(d)
  }
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
  if (c) {
    if (c.pinnedModel) {
      const opt = Array.from(modelSel.options).some(o => o.value === c.pinnedModel)
      if (opt) modelSel.value = c.pinnedModel
    }
    presetSel.value = Array.from(presetSel.options).some(o => o.value === c.presetId) ? c.presetId : ''
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
  const presets = (await App.ListPresets()) || []
  presetSel.innerHTML = `<option value="">No preset</option>` +
    presets.map(p => `<option value="${p.id}">${p.name}</option>`).join('')
  ;(presetSel as any)._presets = presets
}

function currentSystemPrompt(): string {
  const presets = (presetSel as any)._presets || []
  const p = presets.find((x: any) => x.id === presetSel.value)
  return p ? p.systemPrompt : ''
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
    await App.SendMessage(activeConv!, text, currentSystemPrompt(), modelSel.value)
    await App.SetConversationMeta(activeConv!, presetSel.value, modelSel.value)
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

$('newChat').onclick = newChat
sendBtn.onclick = () => { if (streaming) { App.CancelMessage() } else { void send() } }
$('tbBtn').onclick = showTextbooks
$('tbModal').onclick = (e) => { if (e.target === $('tbModal')) $('tbModal').classList.add('hidden') }
input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send()
})

;(async () => {
  const issues = (await App.StartupIssues()) || []
  if (issues.length) addMsg('assistant', '⚠ Setup:\n' + issues.join('\n'))
  await loadMeta()
  await loadConversations()
})()
