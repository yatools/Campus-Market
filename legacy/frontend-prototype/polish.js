/* ================================================================
   梧桐墙 交互与动效增强层 v3（polish.js）
   不侵入既有代码：注入 CSS + 包装全局函数。尊重 prefers-reduced-motion。
   ================================================================ */
(function(){
'use strict';

/* ========== 1. 动效 CSS 注入 ========== */
var css=document.createElement('style');
css.textContent=[
'/* 页面切换过渡 */',
'.page.active{animation:pageIn .28s cubic-bezier(.2,.7,.3,1)}',
'@keyframes pageIn{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:none}}',
'/* 便利贴入场 stagger */',
'.sticky-grid{--stagger:0}',
'.sticky-note{animation:noteIn .4s cubic-bezier(.2,.7,.3,1) backwards}',
'.sticky-note:nth-child(1){animation-delay:.03s}.sticky-note:nth-child(2){animation-delay:.08s}',
'.sticky-note:nth-child(3){animation-delay:.13s}.sticky-note:nth-child(4){animation-delay:.18s}',
'.sticky-note:nth-child(5){animation-delay:.23s}.sticky-note:nth-child(6){animation-delay:.28s}',
'@keyframes noteIn{from{opacity:0;transform:translateY(10px) rotate(0) scale(.96)}}',
'/* 卡片/帖子 hover 抬升 */',
'.card,.post,.ticket{transition:box-shadow .18s,transform .18s,border-color .18s}',
'.post:hover,.ticket:hover{transform:translateY(-2px);box-shadow:0 6px 18px rgba(30,50,40,.09);border-color:#CFCBB8}',
'/* 弹窗弹入 */',
'.overlay{animation:ovIn .18s ease-out}',
'@keyframes ovIn{from{background:rgba(25,35,28,0)}}',
'.overlay .modal{animation:mdIn .22s cubic-bezier(.2,.8,.3,1.1)}',
'@keyframes mdIn{from{opacity:0;transform:translateY(14px) scale(.97)}}',
'/* 按钮按压反馈 */',
'.btn,.btn-post,.chip,.rank-tab,.navitem{transition:transform .08s,background .15s,filter .15s}',
'.btn:active,.btn-post:active,.chip:active{transform:scale(.96)}',
'/* 弹窗打开时锁定背景滚动（:has 现代浏览器） */',
'body:has(.overlay.show){overflow:hidden}',
'/* 点赞飘字 */',
'.float-up{position:fixed;font-weight:800;color:var(--stamp);font-size:14px;pointer-events:none;z-index:300;animation:floatUp .8s ease-out forwards}',
'@keyframes floatUp{to{transform:translateY(-34px);opacity:0}}',
'/* 已点赞状态 */',
'.liked{color:var(--stamp)!important;font-weight:700}',
'/* 倒计时告警：<10 分钟脉冲 */',
'.cd-urgent{animation:cdPulse 1s infinite}',
'@keyframes cdPulse{50%{opacity:.35}}',
'/* 座位点入场 */',
'.seatdots i{animation:dotIn .3s backwards}',
'.seatdots i:nth-child(1){animation-delay:.02s}.seatdots i:nth-child(2){animation-delay:.06s}',
'.seatdots i:nth-child(3){animation-delay:.1s}.seatdots i:nth-child(4){animation-delay:.14s}',
'.seatdots i:nth-child(5){animation-delay:.18s}.seatdots i:nth-child(6){animation-delay:.22s}',
'@keyframes dotIn{from{transform:scale(0)}}',
'/* 骨架屏 */',
'.skel{background:linear-gradient(90deg,#EFEDE0 25%,#F8F6EC 50%,#EFEDE0 75%);background-size:200% 100%;animation:skel 1.1s infinite;border-radius:8px}',
'@keyframes skel{to{background-position:-200% 0}}',
'/* 顶栏通知红点 */',
'.noti-dot{position:absolute;top:-4px;right:-6px;min-width:16px;height:16px;border-radius:8px;background:var(--stamp);color:#fff;font-size:10px;display:flex;align-items:center;justify-content:center;padding:0 4px;font-weight:700}',
'/* 表单校验失败抖动 */',
'.shake{animation:shake .35s}',
'@keyframes shake{20%,60%{transform:translateX(-5px)}40%,80%{transform:translateX(5px)}}',
'.field input.invalid,.field textarea.invalid{border-color:var(--stamp);background:#FFF7F5}',
'/* toast 图标间距 */',
'.toast{display:none;gap:6px}',
'.toast.show{display:flex;align-items:center}',
'/* 搜索过滤时隐藏 */',
'.search-hide{display:none!important}',
'/* 尊重减少动效偏好 */',
'@media (prefers-reduced-motion:reduce){.sticky-note,.page.active,.overlay,.overlay .modal,.seatdots i,.skel,.cd-urgent{animation:none!important}}'
].join('\n');
document.head.appendChild(css);

/* ========== 2. 登录态 UI ========== */
function refreshAuthUI(){
  var link=document.querySelector('.login-link');
  var chip=document.querySelector('.userchip');
  if(!link||!chip)return;
  if(window.TOKEN){
    link.textContent='退出';
    link.onclick=function(){
      localStorage.removeItem('wt_token');window.TOKEN='';
      toast('已退出登录');setTimeout(function(){location.reload()},600);
    };
  }else{
    link.textContent='注册 / 登录';
    link.onclick=function(){openModal('m-register')};
    chip.querySelector('.uname').textContent='未登录';
    chip.querySelector('.umeta').textContent='点击登录';
    chip.querySelector('.avatar').textContent='?';
    chip.onclick=function(){openModal('m-register')};
  }
}
setTimeout(refreshAuthUI,400);

/* ========== 3. Esc 关闭弹窗 ========== */
document.addEventListener('keydown',function(e){
  if(e.key!=='Escape')return;
  var dyn=document.getElementById('m-dynform');
  if(dyn){dyn.remove();return}
  var open=document.querySelector('.overlay.show');
  if(open)open.classList.remove('show');
});

/* ========== 4. 点赞防重复 + 飘字 ========== */
var likedSet={};
try{likedSet=JSON.parse(localStorage.getItem('wt_liked')||'{}')}catch(e){}
function saveLiked(){try{localStorage.setItem('wt_liked',JSON.stringify(likedSet))}catch(e){}}
function floatPlus(el,text){
  var r=el.getBoundingClientRect();
  var s=document.createElement('span');s.className='float-up';s.textContent=text||'+1';
  s.style.left=(r.left+r.width/2)+'px';s.style.top=(r.top-6)+'px';
  document.body.appendChild(s);setTimeout(function(){s.remove()},850);
}
if(window.likePost){
  var _like=window.likePost;
  window.likePost=function(id,el){
    if(likedSet['p'+id]){toast('已经赞过啦');return}
    likedSet['p'+id]=1;saveLiked();
    el.classList.add('liked');floatPlus(el);
    return _like(id,el);
  };
}

/* ========== 5. 车队筛选修复：兼容动态中文 data-game ========== */
window.filterGame=function(btn,key){
  document.querySelectorAll('#gameFilter .chip').forEach(function(c){c.classList.remove('on')});
  btn.classList.add('on');
  var label=btn.textContent.trim();
  document.querySelectorAll('.ticket').forEach(function(t){
    var g=t.dataset.game||'';
    var hit=(key==='all')||g===key||label.indexOf(g)>-1||g.indexOf(label)>-1;
    t.style.display=hit?'flex':'none';
  });
};

/* ========== 6. 倒计时告警（<10 分钟脉冲变红） ========== */
setInterval(function(){
  document.querySelectorAll('.cd[data-dep]').forEach(function(el){
    var s=(new Date(el.dataset.dep)-new Date())/1000;
    el.classList.toggle('cd-urgent',s>0&&s<600);
  });
},1000);

/* ========== 7. 骨架屏：切换到动态板块时先占位 ========== */
var SKEL_PAGES=['qa','handbook','course','observe','gov','lost','activity','announce'];
var _sp=window.showPage;
window.showPage=function(id){
  _sp(id);
  if(window.LIVE&&SKEL_PAGES.indexOf(id)>-1){
    var pg=document.getElementById('page-'+id);
    if(!pg)return;
    var d=pg.querySelector('.dyn');
    if(!d){d=document.createElement('div');d.className='dyn';
      var ph=pg.querySelector('.page-head');
      if(ph)ph.after(d);else pg.prepend(d)}
    if(!d.dataset.loaded){d.dataset.loaded='1';
      d.innerHTML='<div class="skel" style="height:88px;margin-bottom:12px"></div>'
        +'<div class="skel" style="height:88px;margin-bottom:12px;animation-delay:.15s"></div>'
        +'<div class="skel" style="height:88px;animation-delay:.3s"></div>';
    }
  }
};

/* ========== 8. 通知红点（30s 轮询未读数） ========== */
function pollNoti(){
  if(!window.TOKEN)return;
  api('/api/notifications').then(function(list){
    var n=list.filter(function(x){return !x.is_read}).length;
    var chip=document.querySelector('.userchip');
    if(!chip)return;
    chip.style.position='relative';
    var dot=chip.querySelector('.noti-dot');
    if(n>0){
      if(!dot){dot=document.createElement('span');dot.className='noti-dot';chip.appendChild(dot)}
      dot.textContent=n>9?'9+':n;
    }else if(dot)dot.remove();
  }).catch(function(){});
}
setTimeout(pollNoti,1200);setInterval(pollNoti,30000);
/* 进入信用页视为已读 */
var _sp2=window.showPage;
window.showPage=function(id){
  _sp2(id);
  if(id==='credit'&&window.TOKEN){
    api('/api/notifications/read-all','POST').then(function(){
      var dot=document.querySelector('.userchip .noti-dot');if(dot)dot.remove();
    }).catch(function(){});
  }
};

/* ========== 9. 站内搜索：回车过滤当前页 ========== */
var sb=document.querySelector('.searchbox input');
var sbtn=document.querySelector('.searchbox button');
function doSearch(){
  var kw=(sb.value||'').trim().toLowerCase();
  var pg=document.querySelector('.page.active');
  if(!pg)return;
  var items=pg.querySelectorAll('.post,.ticket,.card,.sticky-note');
  var hits=0;
  items.forEach(function(el){
    if(!kw){el.classList.remove('search-hide');return}
    var ok=el.textContent.toLowerCase().indexOf(kw)>-1;
    el.classList.toggle('search-hide',!ok);
    if(ok)hits++;
  });
  if(kw)toast('🔎 本页找到 '+hits+' 条匹配'+(hits===0?'，试试换个词或换个板块':''));
}
if(sb){
  sb.addEventListener('keydown',function(e){if(e.key==='Enter')doSearch()});
  sb.addEventListener('input',function(){if(!sb.value)doSearch()});
  if(sbtn)sbtn.onclick=doSearch;
}

/* ========== 10. 表单校验：必填为空标红抖动 + 防重复提交 ========== */
document.addEventListener('click',function(e){
  var btn=e.target.closest('#df-submit');
  if(!btn)return;
  var modal=btn.closest('.modal');
  var bad=null;
  modal.querySelectorAll('input[id^="df-"],textarea[id^="df-"]').forEach(function(inp){
    inp.classList.remove('invalid');
    if(!inp.value.trim()&&inp.type!=='number'){if(!bad)bad=inp;inp.classList.add('invalid')}
  });
  if(bad){
    e.stopImmediatePropagation();e.preventDefault();
    modal.classList.add('shake');setTimeout(function(){modal.classList.remove('shake')},400);
    bad.focus();toast('还有必填项没填完整');
    return;
  }
  /* 防重复提交：2 秒内禁用 */
  btn.disabled=true;btn.textContent='发布中…';
  setTimeout(function(){btn.disabled=false;btn.textContent='发布'},2000);
},true);

/* ========== 11. 公告条关闭记忆（当日不再显示） ========== */
var nb=document.getElementById('noticeBar');
if(nb){
  var today=new Date().toISOString().slice(0,10);
  if(localStorage.getItem('wt_nb_closed')===today)nb.style.display='none';
  var x=nb.querySelector('.close-nb');
  if(x)x.onclick=function(){nb.style.display='none';try{localStorage.setItem('wt_nb_closed',today)}catch(e){}};
}

/* ========== 12. 重要公告弹窗：确认后当日不再弹 ========== */
try{
  if(localStorage.getItem('wt_ann_ok')){
    var ov=document.getElementById('m-announce');
    if(ov)setTimeout(function(){ov.classList.remove('show')},650);
  }
  var annBtns=document.querySelectorAll('#m-announce .btn.primary');
  annBtns.forEach(function(b){
    var old=b.onclick;
    b.onclick=function(){try{localStorage.setItem('wt_ann_ok','1')}catch(e){};if(old)old.call(b)};
  });
}catch(e){}

console.log('[梧桐墙] 交互增强层已加载');
})();
