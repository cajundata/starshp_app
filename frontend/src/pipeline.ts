import * as App from '../wailsjs/go/appapi/API'
import { store, pipeline } from '../wailsjs/go/models'

const $ = (id: string) => document.getElementById(id) as HTMLElement

const PATHWAYS = [
  { key: '', label: '— unrouted —' },
  { key: 'rapid_brainstorm', label: 'Rapid Brainstorm' },
  { key: 'side_business', label: 'Side Business' },
  { key: 'small_project', label: 'Small Project' },
  { key: 'tech_product', label: 'Technology / Product-Led' },
  { key: 'full_startup', label: 'Full Startup' },
]

const STATUSES = ['raw', 'triaged', 'in_review', 'validating', 'go', 'parked', 'killed']

let ideas: store.Idea[] = []
let selectedId: string | null = null

function fmtDate(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  return d.toISOString().slice(0, 10)
}

function pathwayLabel(key: string): string {
  return PATHWAYS.find(p => p.key === key)?.label ?? key
}

async function loadIdeas() {
  try {
    ideas = (await App.ListIdeas()) || []
  } catch (e: any) {
    $('ideaList').innerHTML = `<p class="pl-error">Could not load ideas: ${e?.userMessage || e}</p>`
    return
  }
  renderIdeaList()
}

function renderIdeaList() {
  const host = $('ideaList')
  host.innerHTML = ''
  if (ideas.length === 0) {
    host.innerHTML = '<p class="pl-empty">No ideas yet. Add one to start the pipeline.</p>'
    $('ideaDetail').innerHTML = ''
    return
  }
  for (const idea of ideas) {
    const row = document.createElement('div')
    row.className = 'idea-row' + (idea.ID === selectedId ? ' selected' : '')
    row.innerHTML =
      `<span class="idea-title">${idea.Title}</span>` +
      `<span class="status-chip status-${idea.Status}">${idea.Status}</span>` +
      `<span class="idea-pathway">${pathwayLabel(idea.Pathway)}</span>` +
      (idea.FinancialFlag ? '<span class="fin-flag" title="Touches financial data">$</span>' : '')
    row.onclick = () => { selectedId = idea.ID; renderIdeaList(); void renderDetail(idea.ID) }
    host.appendChild(row)
  }
}

async function renderDetail(id: string) {
  const idea = ideas.find(i => i.ID === id)
  if (!idea) return
  const detail = $('ideaDetail')

  let crits: store.KillCriterion[] = []
  try { crits = (await App.ListKillCriteria(id)) || [] } catch { /* show empty */ }

  const statusOptions = STATUSES.filter(s => s !== idea.Status)
    .map(s => `<option value="${s}">${s}</option>`).join('')

  detail.innerHTML = `
    <h2>${idea.Title}</h2>
    <p class="idea-summary">${idea.Summary || '<em>No summary.</em>'}</p>
    <div class="detail-row"><label>Pathway</label> ${pathwayLabel(idea.Pathway)}</div>
    <div class="detail-row"><label>Status</label>
      <span class="status-chip status-${idea.Status}">${idea.Status}</span>
      <select id="statusSel"><option value="">Move to…</option>${statusOptions}</select>
    </div>
    <h3>Kill criteria</h3>
    <table class="kc-table"><thead><tr>
      <th>Metric</th><th>Threshold</th><th>Review date</th><th>On miss</th><th></th>
    </tr></thead><tbody id="kcBody">
      ${crits.map(k => `<tr>
        <td>${k.Metric}</td><td>${k.Threshold}</td>
        <td>${fmtDate(k.ReviewDate)}</td><td>${k.OnMiss}</td>
        <td><button class="kc-del" data-id="${k.ID}">✕</button></td></tr>`).join('')}
    </tbody></table>
    <button id="addKcBtn">+ Add kill criterion</button>
  `

  ;($('statusSel') as HTMLSelectElement).onchange = (e) => {
    const to = (e.target as HTMLSelectElement).value
    if (to) void moveStatus(idea.ID, to)
  }
  $('addKcBtn').onclick = () => void addCriterion(idea.ID)
  detail.querySelectorAll('.kc-del').forEach(btn => {
    ;(btn as HTMLElement).onclick = async () => {
      await App.DeleteKillCriterion((btn as HTMLElement).dataset.id!)
      void renderDetail(idea.ID)
    }
  })
}

async function moveStatus(id: string, to: string) {
  let reason = ''
  if (to === 'killed' || to === 'parked') {
    reason = prompt(`Reason for moving to ${to}:`) || ''
    if (!reason) return
  }
  try {
    await App.SetIdeaStatus(id, to, reason)
  } catch (e: any) {
    alert(e?.userMessage || `Could not change status: ${e}`)
    return
  }
  await loadIdeas()
  void renderDetail(id)
}

async function addCriterion(ideaID: string) {
  const metric = prompt('Metric (e.g. "Paid installs"):')
  if (!metric) return
  const threshold = prompt('Threshold (e.g. ">=2 in 30 days"):')
  if (!threshold) return
  const dateStr = prompt('Review date (YYYY-MM-DD):')
  if (!dateStr) return
  const reviewDate = Date.parse(dateStr + 'T00:00:00Z')
  if (isNaN(reviewDate)) { alert('Invalid date.'); return }
  const onMiss = (prompt('On miss — kill, park, or halt:', 'kill') || '').trim()
  try {
    await App.AddKillCriterion(ideaID, metric, threshold, reviewDate, onMiss)
  } catch (e: any) {
    alert(e?.userMessage || `Could not add criterion: ${e}`)
    return
  }
  void renderDetail(ideaID)
}

async function newIdea() {
  const title = prompt('Idea title:')
  if (!title) return
  const summary = prompt('One-line summary (optional):') || ''
  const pathway = (prompt('Pathway key (side_business, small_project, full_startup, …) or blank:') || '').trim()
  const financial = confirm('Does this idea touch financial data? OK = yes.')
  try {
    const created = await App.CreateIdea(title, summary, pathway, financial)
    selectedId = created.ID
  } catch (e: any) {
    alert(e?.userMessage || `Could not create idea: ${e}`)
    return
  }
  await loadIdeas()
  if (selectedId) void renderDetail(selectedId)
}

export function openPipeline() {
  $('pipelineView').classList.remove('hidden')
  void loadIdeas()
  void refreshReviewsDue()
}

function closePipeline() {
  $('pipelineView').classList.add('hidden')
}

// refreshReviewsDue runs the sweep and updates the sidebar badge + panel.
export async function refreshReviewsDue() {
  let due: pipeline.DueReviewView[] = []
  try { due = (await App.ListReviewsDue()) || [] } catch { return }
  const badge = $('reviewsDueBadge')
  if (due.length > 0) {
    badge.textContent = String(due.length)
    badge.classList.remove('hidden')
  } else {
    badge.classList.add('hidden')
  }
  const panel = $('reviewsDuePanel')
  if (due.length === 0) {
    panel.classList.add('hidden')
    panel.innerHTML = ''
    return
  }
  panel.classList.remove('hidden')
  panel.innerHTML =
    `<div class="rd-title">⏰ ${due.length} review${due.length > 1 ? 's' : ''} due</div>` +
    due.map(d => `<div class="rd-row">
      <strong>${d.IdeaTitle}</strong> — ${d.Metric} (${d.Threshold}),
      due ${fmtDate(d.ReviewDate)}${d.DaysOverdue > 0 ? `, ${d.DaysOverdue}d overdue` : ''}
      → on miss: ${d.OnMiss}</div>`).join('')
}

export function initPipeline() {
  $('pipelineBtn').onclick = () => openPipeline()
  $('pipelineBack').onclick = () => closePipeline()
  $('pipelineNewBtn').onclick = () => void newIdea()
}
