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

function addMsg(role: string, text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = `msg ${role}`
  el.textContent = text
  thread.appendChild(el)
  thread.scrollTop = thread.scrollHeight
  return el
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
  for (const m of msgs) addMsg(m.role, m.content)
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
  try { await App.EnsureIndexed(activeConv!) }
  catch (e: any) { addMsg('assistant', `[index] ${e?.userMessage || e}`); }
  const text = input.value.trim()
  input.value = ''
  addMsg('user', text)
  const asst = addMsg('assistant', '')
  streaming = true
  sendBtn.textContent = 'Stop ◼'
  sendBtn.classList.add('streaming')
  try {
    await App.SendMessage(activeConv!, text, currentSystemPrompt(), modelSel.value)
  } catch (e: any) {
    asst.textContent += `\n\n[${e?.code || 'error'}] ${e?.userMessage || e}`
  } finally {
    streaming = false
    sendBtn.textContent = 'Send ▸'
    sendBtn.classList.remove('streaming')
    await loadConversations()
  }
}

EventsOn('chat:token', (tok: string) => {
  const last = thread.querySelector('.msg.assistant:last-child')
  if (last) { last.textContent += tok; thread.scrollTop = thread.scrollHeight }
})

EventsOn('rag:index', (p: any) => {
  const last = thread.querySelector('.msg.assistant:last-child')
  if (last) last.textContent = `Indexing ${p.book}… ${p.done}/${p.total} chapters`
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
    try { await App.EnsureIndexed(activeConv!); banner.textContent = 'Textbooks ready.' }
    catch (e: any) { banner.textContent = `Indexing failed: ${e?.userMessage || e}` }
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
