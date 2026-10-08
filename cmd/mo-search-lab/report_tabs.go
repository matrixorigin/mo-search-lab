// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

const reportTabsHTML = `<nav class="report-tabs" id="report-tabs-navigation" role="tablist" aria-label="报告内容" hidden>
<button type="button" role="tab" id="tab-performance" aria-controls="report-performance" aria-selected="true">性能测试</button>
<button type="button" role="tab" id="tab-environment" aria-controls="report-environment" aria-selected="false" tabindex="-1">运行环境</button>
</nav>`

const reportTabsStyle = `
.report-tabs{display:flex;gap:24px;border-bottom:1px solid oklch(85% .015 248);margin:24px 0 8px}.report-tabs[hidden],[data-report-panel][hidden]{display:none}.report-tabs button{appearance:none;font:600 15px/1.5 system-ui,sans-serif;color:oklch(47% .025 248);border:0;border-bottom:3px solid transparent;background:transparent;cursor:pointer;padding:12px 2px;margin-bottom:-1px}.report-tabs button:hover{color:oklch(34% .08 248)}.report-tabs button[aria-selected="true"]{color:oklch(35% .08 248);border-bottom-color:oklch(48% .1 248)}.report-tabs button:focus-visible{outline:2px solid oklch(53% .12 248);outline-offset:4px;border-radius:2px}.environment-tab-title{font-size:22px;margin:24px 0 4px}.environment-tab-context{font-size:13px;color:oklch(47% .025 248);max-width:75ch;margin:16px 0}.environment-tab-context strong{color:oklch(30% .025 248)}
@media print{.report-tabs{display:none}[data-report-panel][hidden]{display:block!important}}
`

const reportTabsScript = `<script id="report-tabs-controller">
(()=>{
 const nav=document.querySelector('.report-tabs');
 if(!nav)return;
 const tabs=Array.from(nav.querySelectorAll('[role="tab"]'));
 const panels=tabs.map(tab=>document.getElementById(tab.getAttribute('aria-controls')));
 if(panels.some(panel=>!panel))return;
 function select(index,remember=false){
  tabs.forEach((tab,i)=>{tab.setAttribute('aria-selected',String(i===index));tab.tabIndex=i===index?0:-1;panels[i].hidden=i!==index});
  if(remember){try{history.replaceState(null,'','#'+panels[index].id)}catch(e){}}
 }
 function followHash(){
  let id;try{id=decodeURIComponent(location.hash.slice(1))}catch(e){return}
  const target=document.getElementById(id),panel=target&&target.closest('[data-report-panel]');
  const index=panels.indexOf(panel);if(index>=0)select(index);
 }
 tabs.forEach((tab,index)=>{
  tab.addEventListener('click',()=>select(index,true));
  tab.addEventListener('keydown',event=>{
   let next=index;
   if(event.key==='ArrowRight')next=(index+1)%tabs.length;
   else if(event.key==='ArrowLeft')next=(index+tabs.length-1)%tabs.length;
   else if(event.key==='Home')next=0;
   else if(event.key==='End')next=tabs.length-1;
   else return;
   event.preventDefault();select(next,true);tabs[next].focus();
  });
 });
 select(0);nav.hidden=false;followHash();
 window.addEventListener('hashchange',followHash);
})();
</script>`
