const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
let activeJob=null, shortsJob=null, shortsSource='upload';

function icon(id){return `<svg><use href="#${id}"/></svg>`}
function fileLabel(input,target){const f=input.files?.[0];$(target).textContent=f?`${f.name} · ${formatBytes(f.size)}`:'Nenhum arquivo';}
function formatBytes(n){if(!n)return '';const u=['B','KB','MB','GB'];let i=0;while(n>=1024&&i<u.length-1){n/=1024;i++}return `${n.toFixed(i?1:0)} ${u[i]}`}

$$('.tab').forEach(btn=>btn.addEventListener('click',()=>{ $$('.tab').forEach(x=>x.classList.toggle('active',x===btn)); $$('.panel').forEach(x=>x.classList.toggle('active',x.id===`panel-${btn.dataset.tab}`)); const slot=$(`#panel-${btn.dataset.tab} .mixer-slot`); if(slot)slot.appendChild($('#mixer')); }));
$$('.source-btn').forEach(btn=>btn.addEventListener('click',()=>{shortsSource=btn.dataset.source;$$('.source-btn').forEach(x=>x.classList.toggle('active',x===btn));$('#shorts-upload-wrap').classList.toggle('hidden',shortsSource!=='upload');$('#shorts-youtube-wrap').classList.toggle('hidden',shortsSource!=='youtube');}));

function wireDrop(zoneSel,inputSel,nameSel){const zone=$(zoneSel),input=$(inputSel);zone.addEventListener('click',e=>{if(e.target.tagName!=='INPUT')input.click()});['dragenter','dragover'].forEach(ev=>zone.addEventListener(ev,e=>{e.preventDefault();zone.classList.add('drag')}));['dragleave','drop'].forEach(ev=>zone.addEventListener(ev,e=>{e.preventDefault();zone.classList.remove('drag')}));zone.addEventListener('drop',e=>{if(e.dataTransfer.files.length){const dt=new DataTransfer();dt.items.add(e.dataTransfer.files[0]);input.files=dt.files;fileLabel(input,nameSel)}});input.addEventListener('change',()=>fileLabel(input,nameSel));}
wireDrop('#auto-drop','#auto-file','#auto-file-name');wireDrop('#shorts-drop','#shorts-file','#shorts-file-name');
$('#reaction-creator').addEventListener('change',()=>fileLabel($('#reaction-creator'),'#reaction-creator-name'));
$('#reaction-target').addEventListener('change',()=>fileLabel($('#reaction-target'),'#reaction-target-name'));

const STAGES={queued:'Na fila',running:'Processando',probe:'Analisando mídia',audio:'Extraindo áudio',transcribe:'Transcrevendo',silence:'Cortando silêncios',cut:'Aplicando cortes',plan:'Planejando edição',assets:'Buscando B-roll',music:'Escolhendo trilha',captions:'Legenda e textos',render:'Renderizando',mix:'Mixando áudio',analyze:'Assistindo clipes',direct:'Montando roteiro',timeline:'Juntando trechos',done:'Concluído',error:'Erro'};
function stageLabel(s){return STAGES[s]||s||'Preparando'}
function showJob(){const c=$('#job-card');c.classList.remove('hidden');$('#job-error').textContent='';$('#job-actions').innerHTML='';}
function paintJob(j){showJob();$('#job-fill').style.width=`${j.percent||0}%`;$('#job-percent').textContent=`${j.percent||0}%`;$('#job-stage').textContent=stageLabel(j.stage||j.status);$('#job-detail').textContent=j.detail||'';$('#job-error').textContent=j.error||'';if(j.status==='done'&&j.download){$('#job-actions').innerHTML=`<a class="button primary" href="${j.download}">${icon('i-download')}Baixar vídeo final</a>`;}}
async function createJob(url,fd){showJob();const r=await fetch(url,{method:'POST',body:fd});if(!r.ok)throw new Error(await r.text());const j=await r.json();activeJob=j.id;paintJob(j);return j;}
async function pollJob(id,stopStatuses=['done','error']){for(;;){const r=await fetch(`/api/jobs/${id}`);if(!r.ok)throw new Error(await r.text());const j=await r.json();paintJob(j);if(stopStatuses.includes(j.status))return j;await new Promise(r=>setTimeout(r,900));}}
function report(err){showJob();$('#job-error').textContent=err?.message||String(err)}

// ---- Mixer de áudio: a voz sempre prevalece (limites também garantidos no servidor)
const mixIds=['music-db','duck-db','voice-db','sfx-db'];
function fmtDB(v){v=Number(v);return `${v>0?'+':''}${v} dB`}
function paintMixer(){
  const m=$('#mix-music-db').value,d=$('#mix-duck-db').value,v=$('#mix-voice-db').value,s=$('#mix-sfx-db').value,on=$('#mix-music').checked;
  $('#mix-music-db-out').textContent=`${Math.abs(m)} dB abaixo da voz`;
  $('#mix-duck-db-out').textContent=`−${d} dB ao falar`;
  $('#mix-voice-db-out').textContent=fmtDB(v);
  $('#mix-sfx-db-out').textContent=fmtDB(s);
  $$('[data-needs-music]').forEach(x=>x.classList.toggle('disabled',!on));
  $$('[data-needs-music] input,[data-needs-music] select').forEach(x=>x.disabled=!on);
}
function setMixer(a){if(!a)return;if(a.gain_db!=null)$('#mix-music-db').value=a.gain_db;if(a.duck_db!=null)$('#mix-duck-db').value=a.duck_db;if(a.voice_gain_db!=null)$('#mix-voice-db').value=a.voice_gain_db;if(a.sfx_gain_db!=null)$('#mix-sfx-db').value=a.sfx_gain_db;if(a.enabled!=null)$('#mix-music').checked=!!a.enabled;if(a.mood)$('#mix-mood').value=a.mood;paintMixer();}
function mixerValues(){return{music_enabled:$('#mix-music').checked,music_db:Number($('#mix-music-db').value),duck_db:Number($('#mix-duck-db').value),voice_db:Number($('#mix-voice-db').value),sfx_db:Number($('#mix-sfx-db').value),mood:$('#mix-mood').value}}
mixIds.forEach(id=>$('#mix-'+id).addEventListener('input',()=>{$$('.preset').forEach(p=>p.classList.remove('active'));paintMixer();}));
$('#mix-music').addEventListener('change',paintMixer);
$$('.preset').forEach(p=>p.addEventListener('click',()=>{$('#mix-music-db').value=p.dataset.music;$('#mix-duck-db').value=p.dataset.duck;$('#mix-voice-db').value=p.dataset.voice;$('#mix-sfx-db').value=p.dataset.sfx;$$('.preset').forEach(x=>x.classList.toggle('active',x===p));paintMixer();}));
paintMixer();

function paintAutoDone(j){
  if(j.status!=='done'||!j.video)return;
  const src=`${j.video}?t=${Date.now()}`;
  $('#job-actions').innerHTML=`<video class="result-video" controls playsinline src="${src}"></video><div class="job-actions-row"><a class="button primary" href="${j.download}">${icon('i-download')}Baixar vídeo final</a><button class="button secondary" id="remix-run">${icon('i-gauge')}Aplicar mixer e re-renderizar</button></div><small class="mix-hint">Ajuste o mixer acima e re-renderize: só o áudio é refeito (sem transcrever de novo).</small>`;
  $('#remix-run').addEventListener('click',()=>remix(j.id));
}
async function remix(id){try{$('#remix-run').disabled=true;const r=await fetch(`/api/jobs/${id}/remix`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(mixerValues())});if(!r.ok)throw new Error(await r.text());paintJob(await r.json());const done=await pollJob(id);setMixer(done.audio);paintAutoDone(done);paintAssemblyPlan(done);}catch(e){report(e)}}

$('#auto-run').addEventListener('click',async()=>{try{const f=$('#auto-file').files[0];if(!f)throw new Error('Selecione um vídeo.');const fd=new FormData();fd.append('video',f);const mv=mixerValues();Object.entries(mv).forEach(([k,v])=>fd.append(k,String(v)));const j=await createJob('/api/jobs',fd);const done=await pollJob(j.id);setMixer(done.audio);paintAutoDone(done);}catch(e){report(e)}});
$('#reaction-run').addEventListener('click',async()=>{try{const a=$('#reaction-creator').files[0],b=$('#reaction-target').files[0];if(!a||!b)throw new Error('Selecione seu vídeo e o vídeo para reagir.');const fd=new FormData();fd.append('creator',a);fd.append('reaction',b);fd.append('audio_mode',$('#reaction-audio').value);const j=await createJob('/api/reactions',fd);await pollJob(j.id);}catch(e){report(e)}});

$('#shorts-analyze').addEventListener('click',async()=>{try{const fd=new FormData();fd.append('count',$('#shorts-count').value);if(shortsSource==='upload'){const f=$('#shorts-file').files[0];if(!f)throw new Error('Selecione um vídeo longo.');fd.append('video',f)}else{const u=$('#shorts-url').value.trim();if(!u)throw new Error('Informe a URL do YouTube.');fd.append('youtube_url',u)}const j=await createJob('/api/shorts/analyze',fd);shortsJob=j.id;const done=await pollJob(j.id,['analyzed','error']);if(done.status==='analyzed')renderCandidates(done);}catch(e){report(e)}});

function scoreLine(label,v){return `<div class="score"><span>${label} <b>${v}/25</b></span><div class="scorebar"><i style="width:${v/25*100}%"></i></div></div>`}
function renderCandidates(j){const box=$('#shorts-results');box.classList.remove('hidden');box.innerHTML=`<div class="results-head"><div><h3>${j.candidates.length} cortes encontrados</h3><div class="clip-meta">Score editorial: auxílio de seleção, não promessa de viralização.</div></div><button class="button primary" id="generate-selected">${icon('i-sparkles')}Gerar selecionados</button></div><div class="clip-list">${j.candidates.map((c,i)=>`<article class="clip-card"><input class="clip-check" type="checkbox" data-id="${c.id}" ${i<3?'checked':''}><div><h4>${escapeHTML(c.title)}</h4><div class="clip-meta">${fmtTime(c.start)} → ${fmtTime(c.end)} · ${c.duration.toFixed(1)} s · ${c.hook_type}</div></div><div class="score-total"><strong>${c.scores.total}</strong><span>/100</span></div><div class="score-grid">${scoreLine('Hook',c.scores.hook)}${scoreLine('Engajamento',c.scores.engagement)}${scoreLine('Valor',c.scores.value)}${scoreLine('Share',c.scores.shareability)}</div><div class="clip-reason">${escapeHTML(c.reason)}</div><div class="clip-actions"><button class="mini preview" data-id="${c.id}" data-title="${escapeAttr(c.title)}">${icon('i-eye')}Preview</button><button class="mini one" data-id="${c.id}">${icon('i-play')}Gerar só este</button></div></article>`).join('')}</div>`;$('#generate-selected').addEventListener('click',()=>generateShorts($$('.clip-check:checked').map(x=>x.dataset.id)));$$('.preview').forEach(b=>b.addEventListener('click',()=>openPreview(b.dataset.id,b.dataset.title)));$$('.one').forEach(b=>b.addEventListener('click',()=>generateShorts([b.dataset.id])));}

async function generateShorts(ids){try{if(!shortsJob)throw new Error('Analise um vídeo primeiro.');if(!ids.length)throw new Error('Selecione pelo menos um corte.');const r=await fetch(`/api/shorts/${shortsJob}/generate`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids})});if(!r.ok)throw new Error(await r.text());paintJob(await r.json());const done=await pollJob(shortsJob);if(done.status==='done'&&done.clips?.length){$('#job-actions').innerHTML=done.clips.map(c=>`<a class="button secondary" href="${c.download}">${icon('i-download')}${escapeHTML(c.title)}</a>`).join('')}}catch(e){report(e)}}
function openPreview(id,title){const m=$('#preview-modal'),v=$('#preview-video');$('#preview-title').textContent=title;v.src=`/api/shorts/${shortsJob}/preview/${id}`;m.classList.remove('hidden');v.play().catch(()=>{})}
function closePreview(){const v=$('#preview-video');v.pause();v.removeAttribute('src');v.load();$('#preview-modal').classList.add('hidden')}
$('#preview-close').addEventListener('click',closePreview);$('#preview-modal').addEventListener('click',e=>{if(e.target===$('#preview-modal'))closePreview()});
function fmtTime(s){s=Math.max(0,Math.round(s));const m=Math.floor(s/60),sec=s%60;return `${String(m).padStart(2,'0')}:${String(sec).padStart(2,'0')}`}
function escapeHTML(s=''){return s.replace(/[&<>"']/g,m=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'}[m]))}
function escapeAttr(s=''){return escapeHTML(s).replace(/`/g,'&#096;')}

// ---- Montagem IA: vários clipes + prompt -> um vídeo editado
let assemblyFiles=[];
function renderQueue(){
  const q=$('#assembly-queue');
  q.innerHTML=assemblyFiles.map((f,i)=>`<li class="clip-item"><span class="clip-index">${i+1}</span><span class="clip-name">${escapeHTML(f.name)}</span><small>${formatBytes(f.size)}</small><span class="clip-tools"><button type="button" class="mini-icon" data-act="up" data-i="${i}" aria-label="Mover para cima" ${i===0?'disabled':''}>${icon('i-up')}</button><button type="button" class="mini-icon" data-act="down" data-i="${i}" aria-label="Mover para baixo" ${i===assemblyFiles.length-1?'disabled':''}>${icon('i-down')}</button><button type="button" class="mini-icon" data-act="del" data-i="${i}" aria-label="Remover">${icon('i-x')}</button></span></li>`).join('');
  q.classList.toggle('hidden',!assemblyFiles.length);
  $$('#assembly-queue button').forEach(b=>b.addEventListener('click',e=>{e.preventDefault();const i=+b.dataset.i;if(b.dataset.act==='del')assemblyFiles.splice(i,1);else{const j=b.dataset.act==='up'?i-1:i+1;[assemblyFiles[i],assemblyFiles[j]]=[assemblyFiles[j],assemblyFiles[i]];}renderQueue();}));
}
function addAssemblyFiles(list){for(const f of list){if(f.type.startsWith('video/')||/\.(mp4|mov|m4v|webm|mkv)$/i.test(f.name))assemblyFiles.push(f)}renderQueue();}
(()=>{const zone=$('#assembly-drop'),input=$('#assembly-files');
  zone.addEventListener('click',e=>{if(e.target.tagName!=='INPUT')input.click()});
  ['dragenter','dragover'].forEach(ev=>zone.addEventListener(ev,e=>{e.preventDefault();zone.classList.add('drag')}));
  ['dragleave','drop'].forEach(ev=>zone.addEventListener(ev,e=>{e.preventDefault();zone.classList.remove('drag')}));
  zone.addEventListener('drop',e=>addAssemblyFiles(e.dataTransfer.files));
  input.addEventListener('change',()=>{addAssemblyFiles(input.files);input.value='';});
  renderQueue();
})();
$$('.example').forEach(b=>b.addEventListener('click',()=>{$('#assembly-prompt').value=b.textContent;$('#assembly-prompt').focus();}));

function roleLabel(r){return({hook:'Gancho',build:'Desenvolvimento',punchline:'Punchline',outro:'Fechamento'})[r]||'Trecho'}
function paintAssemblyPlan(j){
  const p=j.assembly;if(!p)return;
  const names=Object.fromEntries((p.clips||[]).map(c=>[c.id,c.name]));
  const rows=p.segments.map((s,i)=>`<li><b>${i+1}. ${roleLabel(s.role)}</b> · ${escapeHTML(names[s.clip_id]||s.clip_id)} <span class="seg-time">${s.start.toFixed(1)}s → ${s.end.toFixed(1)}s</span>${s.text?`<div class="seg-text">“${escapeHTML(s.text)}”</div>`:''}${s.reason?`<div class="seg-reason">${escapeHTML(s.reason)}</div>`:''}</li>`).join('');
  const box=document.createElement('div');box.className='assembly-plan';
  box.innerHTML=`<h3>${escapeHTML(p.title||'Roteiro da montagem')}</h3><div class="plan-meta">Editor: ${p.origin==='ollama'?'IA (Ollama)':'heurístico'} · trilha ${escapeHTML(p.music_mood||'')} · ${p.segments.length} trechos</div>${p.notes?`<p class="plan-notes">${escapeHTML(p.notes)}</p>`:''}<ol>${rows}</ol>`;
  $('#job-actions').appendChild(box);
}
$('#assembly-run').addEventListener('click',async()=>{try{
  if(!assemblyFiles.length)throw new Error('Adicione pelo menos um clipe.');
  const prompt=$('#assembly-prompt').value.trim();if(!prompt)throw new Error('Descreva no prompt o vídeo que você quer.');
  const fd=new FormData();assemblyFiles.forEach(f=>fd.append('clips',f));fd.append('prompt',prompt);fd.append('duration',$('#assembly-duration').value);fd.append('captions',$('#assembly-captions').value);
  Object.entries(mixerValues()).forEach(([k,v])=>fd.append(k,String(v)));
  const j=await createJob('/api/assemble',fd);const done=await pollJob(j.id);setMixer(done.audio);paintAutoDone(done);paintAssemblyPlan(done);
}catch(e){report(e)}});
