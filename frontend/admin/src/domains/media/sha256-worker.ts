import {IncrementalSHA256} from "./sha256";

type Input={file:File;partBytes:number};
self.onmessage = async (event:MessageEvent<Input>) => {
 try {
  const {file,partBytes}=event.data;
  if(!Number.isSafeInteger(partBytes)||partBytes<1)throw new Error("Некорректный размер части");
  const full=new IncrementalSHA256();const parts:string[]=[];
  // Transmitted parts may be gigabytes; calculate them in small bounded
  // streaming blocks so the configured part size does not become a RAM limit.
  const hashBlockBytes=1024*1024;
  for(let offset=0;offset<file.size;offset+=partBytes){
   const end=Math.min(file.size,offset+partBytes);const part=new IncrementalSHA256();
   for(let cursor=offset;cursor<end;cursor+=hashBlockBytes){
    const bytes=new Uint8Array(await file.slice(cursor,Math.min(end,cursor+hashBlockBytes)).arrayBuffer());
    full.update(bytes);part.update(bytes);
    self.postMessage({type:"progress",bytes:cursor+bytes.length,total:file.size});
   }
   parts.push(part.hex());
  }
  self.postMessage({type:"result",sha256:full.hex(),parts});
 } catch(error) {self.postMessage({type:"error",message:error instanceof Error?error.message:"Не удалось проверить файл"});}
};
