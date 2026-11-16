import {describe,expect,it} from "vitest";
import {createHash} from "node:crypto";
import {IncrementalSHA256} from "./sha256";
describe("incremental SHA-256",()=>{
 it.each([0,1,3,55,56,63,64,65,127,128,129,1000,65537])("matches SHA-256 with arbitrary chunking (%i bytes)",size=>{
  const input=Uint8Array.from({length:size},(_,i)=>(i*71+11)&255);const hash=new IncrementalSHA256();for(let i=0;i<input.length;i+=37)hash.update(input.subarray(i,i+37));
  expect(hash.hex()).toBe(createHash("sha256").update(input).digest("hex"));
 });
});
