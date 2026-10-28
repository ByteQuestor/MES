'use strict';

/* ---------- 工具 ---------- */
const $  = (sel, el = document) => el.querySelector(sel);
const $$ = (sel, el = document) => [...el.querySelectorAll(sel)];
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const pad = (n, w = 2) => String(n).padStart(w, '0');

function fmtTime(iso) {
  if (!iso) return '-';
  const d = new Date(iso);
  if (isNaN(d)) return '-';
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

async function api(path, opts = {}) {
  const res = await fetch(path, Object.assign({ headers: { 'Content-Type': 'application/json' } }, opts));
  let j;
  try { j = await res.json(); } catch { throw new Error('服务响应异常 (HTTP ' + res.status + ')'); }
  if (j.code !== 0) throw new Error(j.message || '操作失败');
  return j.data;
}

let toastTimer;
function toast(msg) {
  let el = $('#toast');
  if (!el) { el = document.createElement('div'); el.id = 'toast'; document.body.appendChild(el); }
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2000);
}

/* ---------- 标签页切换 ---------- */
$$('.tab').forEach(btn => btn.addEventListener('click', () => {
  $$('.tab').forEach(b => b.classList.toggle('active', b === btn));
  $$('.tab-panel').forEach(p => p.classList.toggle('active', p.id === 'tab-' + btn.dataset.tab));
  if (btn.dataset.tab === 'logs') loadLogs();
}));

/* ================= 料盘管理 ================= */
let trays = [];

async function loadTrays() {
  try { trays = await api('/api/trays'); renderTrays(); }
  catch (e) { toast('加载失败: ' + e.message); }
}

function renderTrays() {
  const kw = $('#searchInput').value.trim().toLowerCase();
  const list = trays.filter(t => !kw
    || (t.boxBarcode || '').toLowerCase().includes(kw)
    || (t.materials || []).some(m => (m.barcode || '').toLowerCase().includes(kw)));

  $('#trayCount').textContent = `共 ${list.length} 盘料`;
  $('#trayEmpty').hidden = list.length > 0;
  $('#trayTbody').innerHTML = list.map(t => {
    const stat = {};
    (t.materials || []).forEach(m => { const k = (m.status || '').trim(); stat[k] = (stat[k] || 0) + 1; });
    return `<tr data-id="${t.id}">
      <td class="mono strong">${esc(t.boxBarcode)}</td>
      <td>${(t.materials || []).length}</td>
      <td>${Object.entries(stat).slice(0, 6).map(([k, n]) => `<span class="chip">${esc(k || '空')}×${n}</span>`).join('')}</td>
      <td class="muted">${esc(t.remark || '')}</td>
      <td class="muted">${fmtTime(t.updatedAt)}</td>
      <td class="w-actions">
        <button class="btn sm" data-act="edit">编辑</button>
        <button class="btn sm" data-act="copy">复制</button>
        <button class="btn sm danger" data-act="del">删除</button>
      </td>
    </tr>`;
  }).join('');
}

$('#searchInput').addEventListener('input', renderTrays);
$('#btnRefreshTrays').addEventListener('click', loadTrays);
$('#btnNew').addEventListener('click', () => openEditor(null));

$('#trayTbody').addEventListener('click', async e => {
  const btn = e.target.closest('button[data-act]');
  if (!btn) return;
  const id = btn.closest('tr').dataset.id;
  const t = trays.find(x => x.id === id);
  if (!t) return;
  if (btn.dataset.act === 'edit') openEditor(t);
  else if (btn.dataset.act === 'copy') openEditor(t, true);
  else if (btn.dataset.act === 'del') {
    if (!confirm(`确认删除料盘 ${t.boxBarcode} ？`)) return;
    try { await api('/api/trays/' + encodeURIComponent(id), { method: 'DELETE' }); loadTrays(); refreshHealth(); }
    catch (err) { alert(err.message); }
  }
});

/* ================= 编辑弹窗 ================= */
let edit = null; // {id, boxBarcode, remark, materials:[]}

function openEditor(t, asCopy) {
  edit = {
    id: t && !asCopy ? t.id : null,
    boxBarcode: t && !asCopy ? t.boxBarcode : '',
    remark: t ? (t.remark || '') : '',
    materials: t ? (t.materials || []).map(m => ({ barcode: m.barcode, status: m.status })) : []
  };
  $('#editorTitle').textContent = edit.id ? '编辑料盘' : (asCopy ? '复制料盘（请修改框体条码）' : '新建料盘');
  $('#editBox').value = edit.boxBarcode;
  $('#editRemark').value = edit.remark;
  $('#importText').value = '';
  renderMats();
  $('#editorMask').hidden = false;
  $('#editBox').focus();
}

function closeEditor() { $('#editorMask').hidden = true; edit = null; }

function renderMats() {
  $('#matTbody').innerHTML = edit.materials.map((m, i) => `
    <tr>
      <td class="muted">${i + 1}</td>
      <td><input class="input mono" data-idx="${i}" data-field="barcode" value="${esc(m.barcode)}" placeholder="物料条码"></td>
      <td><input class="input" data-idx="${i}" data-field="status" value="${esc(m.status)}" list="statusList" placeholder="状态码"></td>
      <td><button class="btn sm danger" data-del="${i}" title="删除此行">删</button></td>
    </tr>`).join('');
  $('#matEmpty').hidden = edit.materials.length > 0;
  $('#editCount').textContent = `共 ${edit.materials.length} 个物料`;
}

$('#matTbody').addEventListener('input', e => {
  const el = e.target;
  if (!el.dataset.field || !edit) return;
  edit.materials[+el.dataset.idx][el.dataset.field] = el.value;
});
$('#matTbody').addEventListener('click', e => {
  const del = e.target.closest('button[data-del]');
  if (!del || !edit) return;
  edit.materials.splice(+del.dataset.del, 1);
  renderMats();
});

$('#btnAddRow').addEventListener('click', () => {
  edit.materials.push({ barcode: '', status: $('#genStatus').value.trim() || 'OK' });
  renderMats();
});

$('#btnRandBox').addEventListener('click', () => {
  let s = '';
  for (let i = 0; i < 10; i++) s += Math.floor(Math.random() * 10);
  $('#editBox').value = 'BOX' + s;
});

$('#btnGen').addEventListener('click', () => {
  const prefix = $('#genPrefix').value.trim() || 'BAT';
  const start = Math.max(1, parseInt($('#genStart').value) || 1);
  const count = Math.min(500, Math.max(1, parseInt($('#genCount').value) || 1));
  const status = $('#genStatus').value.trim();
  const width = Math.max(4, String(start + count - 1).length);
  for (let i = 0; i < count; i++) edit.materials.push({ barcode: prefix + pad(start + i, width), status });
  renderMats();
  toast(`已生成 ${count} 行`);
});

$('#btnImport').addEventListener('click', () => {
  const lines = $('#importText').value.split(/\r?\n/).map(l => l.trim()).filter(Boolean);
  let added = 0, skipped = 0;
  const seen = new Set(edit.materials.map(m => m.barcode));
  for (const line of lines) {
    const parts = line.split(/[,;\t ]+/).map(s => s.trim()).filter(Boolean);
    if (!parts[0]) continue;
    if (seen.has(parts[0])) { skipped++; continue; }
    seen.add(parts[0]);
    edit.materials.push({ barcode: parts[0], status: parts[1] || $('#genStatus').value.trim() || 'OK' });
    added++;
  }
  $('#importText').value = '';
  renderMats();
  toast(`已导入 ${added} 行${skipped ? `，跳过重复 ${skipped} 行` : ''}`);
});

$('#btnCloseEditor').addEventListener('click', closeEditor);
$('#btnCancelEditor').addEventListener('click', closeEditor);
$('#editorMask').addEventListener('click', e => { if (e.target === e.currentTarget) closeEditor(); });
document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('#editorMask').hidden) closeEditor(); });

$('#btnSave').addEventListener('click', async () => {
  edit.boxBarcode = $('#editBox').value.trim();
  edit.remark = $('#editRemark').value.trim();
  edit.materials = edit.materials.map(m => ({ barcode: m.barcode.trim(), status: m.status.trim() }));
  if (!edit.boxBarcode) { alert('框体条码不能为空'); return; }
  if (edit.materials.some(m => !m.barcode)) { alert('存在物料条码为空的行，请填写或删除'); return; }
  const body = JSON.stringify({ boxBarcode: edit.boxBarcode, remark: edit.remark, materials: edit.materials });
  try {
    if (edit.id) await api('/api/trays/' + encodeURIComponent(edit.id), { method: 'PUT', body });
    else await api('/api/trays', { method: 'POST', body });
    closeEditor();
    loadTrays();
    refreshHealth();
    toast('保存成功');
  } catch (e) { alert(e.message); }
});

/* ================= 查询测试 ================= */
async function doQuery() {
  const code = $('#queryInput').value.trim();
  if (!code) { toast('请输入条码'); return; }
  const btn = $('#btnQuery');
  btn.disabled = true;
  const t0 = performance.now();
  try {
    const res = await fetch('/api/query?barcode=' + encodeURIComponent(code));
    const ms = Math.round(performance.now() - t0);
    const j = await res.json();
    const d = j.data || {};
    let meta = '';
    if (d.found) {
      meta += `<span class="badge ok">✓ 命中</span>
        <span class="badge">匹配方式：${d.matchType === 'box' ? '框体条码' : '物料条码'}</span>
        <span class="badge">框体：${esc(d.tray.boxBarcode)}（${(d.tray.materials || []).length} 个物料）</span>`;
    } else {
      meta += `<span class="badge bad">✗ 未命中</span><span class="badge">MES 中没有该条码</span>`;
    }
    meta += `<span class="badge muted">HTTP ${res.status} · code ${j.code} · ${ms} ms</span>`;
    $('#queryMeta').innerHTML = meta;
    const box = $('#queryJson');
    box.hidden = false;
    box.innerHTML = colorJson(j);
  } catch (e) {
    $('#queryMeta').innerHTML = `<span class="badge bad">请求失败：${esc(e.message)}</span>`;
    $('#queryJson').hidden = true;
  } finally { btn.disabled = false; }
}
$('#btnQuery').addEventListener('click', doQuery);
$('#queryInput').addEventListener('keydown', e => { if (e.key === 'Enter') doQuery(); });

function colorJson(obj) {
  const s = JSON.stringify(obj, null, 2).replace(/&/g, '&amp;').replace(/</g, '&lt;');
  return s.replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false)\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g, (m, str, colon) => {
    if (str) return colon ? `<span class="jk">${str}</span>${colon}` : `<span class="js">${str}</span>`;
    if (m === 'true' || m === 'false') return `<span class="jb">${m}</span>`;
    if (m === 'null') return `<span class="jn">${m}</span>`;
    return `<span class="ji">${m}</span>`;
  });
}

/* ================= 查询日志 ================= */
async function loadLogs() {
  try {
    const logs = await api('/api/logs');
    $('#logEmpty').hidden = logs.length > 0;
    $('#logTbody').innerHTML = logs.map(l => `
      <tr>
        <td class="muted mono">${fmtTime(l.time)}</td>
        <td class="mono">${esc(l.barcode)}</td>
        <td>${l.found ? '<span class="badge ok">命中</span>' : '<span class="badge bad">未命中</span>'}</td>
        <td>${l.found ? (l.matchType === 'box' ? '框体条码' : '物料条码') : '-'}</td>
        <td class="mono">${esc(l.boxBarcode || '-')}</td>
        <td class="muted mono">${esc(l.clientIp || '-')}</td>
      </tr>`).join('');
  } catch { /* 静默，下次自动刷新再试 */ }
}

$('#btnRefreshLogs').addEventListener('click', loadLogs);
$('#btnClearLogs').addEventListener('click', async () => {
  if (!confirm('确认清空全部查询日志？')) return;
  try { await api('/api/logs', { method: 'DELETE' }); loadLogs(); } catch (e) { alert(e.message); }
});
setInterval(() => {
  if ($('#tab-logs').classList.contains('active') && !document.hidden) loadLogs();
}, 5000);

/* ================= 顶部服务信息 ================= */
async function refreshHealth() {
  try {
    const h = await api('/api/health');
    $('#serverInfo').innerHTML =
      `v${esc(h.version)} · 数据文件 <span class="mono" title="${esc(h.dataFile)}">${esc(h.dataFileName)}</span> · <b>${h.trays}</b> 盘料`;
  } catch { /* 忽略 */ }
}

/* ---------- 启动 ---------- */
refreshHealth();
loadTrays();
