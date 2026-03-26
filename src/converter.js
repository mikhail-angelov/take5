import ffmpeg from 'fluent-ffmpeg';
import { execSync } from 'child_process';
import fs from 'fs/promises';
import path from 'path';

function ffmpegAvailable() {
  try { execSync('ffmpeg -version', { stdio: 'ignore' }); return true; } catch { return false; }
}

export async function convertToMp4(webmPath, trimStartSeconds = 0, actionSegments = null, recordingStartTime = null) {
  if (!ffmpegAvailable()) {
    console.warn('\n⚠️  ffmpeg not found. Keeping .webm file.');
    console.warn('   Install: brew install ffmpeg  |  apt install ffmpeg');
    console.warn(`   Manual: ffmpeg -i "${webmPath}" output.mp4`);
    return null;
  }

  const mp4Path = webmPath.replace(/\.webm$/, '.mp4');

  // If we have action segments and recording start time, use complex filter to cut delays
  if (actionSegments && actionSegments.length > 0 && recordingStartTime) {
    return await cutDelaysAndConvert(webmPath, mp4Path, actionSegments, recordingStartTime);
  }

  // Otherwise use simple trim
  return new Promise((resolve, reject) => {
    const ffmpegCmd = ffmpeg(webmPath);
    
    // Add trim option if we need to cut the beginning
    if (trimStartSeconds > 0) {
      console.log(`\n✂️  Trimming first ${trimStartSeconds.toFixed(1)} seconds from video...`);
      ffmpegCmd.seekInput(trimStartSeconds);
    }
    
    ffmpegCmd
      .videoCodec('libx264')
      .outputOptions(['-crf 18', '-preset fast', '-pix_fmt yuv420p', '-movflags +faststart', '-an'])
      .on('start', () => process.stdout.write('\n🎞  Converting to mp4...'))
      .on('progress', p => { if (p.percent) process.stdout.write(`\r🎞  Converting: ${Math.round(p.percent)}%   `); })
      .on('end', () => { 
        process.stdout.write('\n'); 
        if (trimStartSeconds > 0) {
          console.log(`✅  Video trimmed: removed first ${trimStartSeconds.toFixed(1)} seconds`);
        }
        resolve(mp4Path); 
      })
      .on('error', err => { console.error('\nffmpeg error:', err.message); reject(err); })
      .save(mp4Path);
  });
}

/**
 * Complex video processing: cut out delays between action segments
 */
async function cutDelaysAndConvert(webmPath, mp4Path, actionSegments, recordingStartTime) {
  console.log(`\n✂️  Cutting delays between ${actionSegments.length} action segments...`);
  
  // Convert timestamps to seconds relative to recording start
  const segmentsInSeconds = actionSegments.map(seg => ({
    start: (seg.start - recordingStartTime) / 1000,
    end: (seg.end - recordingStartTime) / 1000,
    duration: (seg.end - seg.start) / 1000
  }));
  
  // Log segment info
  segmentsInSeconds.forEach((seg, i) => {
    console.log(`   Segment ${i + 1}: ${seg.start.toFixed(1)}s - ${seg.end.toFixed(1)}s (${seg.duration.toFixed(1)}s)`);
  });
  
  const totalActionTime = segmentsInSeconds.reduce((sum, seg) => sum + seg.duration, 0);
  console.log(`   Total action time: ${totalActionTime.toFixed(1)}s`);
  
  // Create a temporary directory for segment files
  const tempDir = path.join(path.dirname(webmPath), 'temp_segments');
  await fs.mkdir(tempDir, { recursive: true });
  
  try {
    // Extract each segment
    const segmentFiles = [];
    for (let i = 0; i < segmentsInSeconds.length; i++) {
      const seg = segmentsInSeconds[i];
      const segmentFile = path.join(tempDir, `segment_${i}.mp4`);
      segmentFiles.push(segmentFile);
      
      await new Promise((resolve, reject) => {
        ffmpeg(webmPath)
          .seekInput(seg.start)
          .duration(seg.duration)
          .videoCodec('libx264')
          .outputOptions(['-crf 18', '-preset fast', '-pix_fmt yuv420p', '-movflags +faststart', '-an'])
          .on('start', () => process.stdout.write(`\r🎞  Extracting segment ${i + 1}/${segmentsInSeconds.length}...`))
          .on('end', () => resolve())
          .on('error', err => reject(err))
          .save(segmentFile);
      });
    }
    
    console.log('\n🎞  Concatenating segments...');
    
    // Create a file list for concatenation
    const listFile = path.join(tempDir, 'concat_list.txt');
    const listContent = segmentFiles.map(file => `file '${file}'`).join('\n');
    await fs.writeFile(listFile, listContent);
    
    // Concatenate all segments
    await new Promise((resolve, reject) => {
      ffmpeg()
        .input(listFile)
        .inputOptions(['-f', 'concat', '-safe', '0'])
        .videoCodec('libx264')
        .outputOptions(['-crf 18', '-preset fast', '-pix_fmt yuv420p', '-movflags +faststart', '-an'])
        .on('start', () => process.stdout.write('\n🎞  Converting to final mp4...'))
        .on('progress', p => { if (p.percent) process.stdout.write(`\r🎞  Converting: ${Math.round(p.percent)}%   `); })
        .on('end', () => { 
          process.stdout.write('\n'); 
          console.log(`✅  Video processed: removed delays between ${segmentsInSeconds.length} actions`);
          console.log(`   Original duration: ${segmentsInSeconds[segmentsInSeconds.length - 1].end.toFixed(1)}s`);
          console.log(`   Final duration: ${totalActionTime.toFixed(1)}s`);
          console.log(`   Time saved: ${(segmentsInSeconds[segmentsInSeconds.length - 1].end - totalActionTime).toFixed(1)}s`);
          resolve(); 
        })
        .on('error', err => reject(err))
        .save(mp4Path);
    });
    
    // Clean up temporary files
    await fs.rm(tempDir, { recursive: true, force: true });
    
    return mp4Path;
    
  } catch (error) {
    // Clean up on error
    try { await fs.rm(tempDir, { recursive: true, force: true }); } catch {}
    console.error('\n❌  Error cutting delays:', error.message);
    console.log('   Falling back to simple conversion...');
    
    // Fall back to simple conversion
    return new Promise((resolve, reject) => {
      ffmpeg(webmPath)
        .videoCodec('libx264')
        .outputOptions(['-crf 18', '-preset fast', '-pix_fmt yuv420p', '-movflags +faststart', '-an'])
        .on('start', () => process.stdout.write('\n🎞  Converting to mp4 (fallback)...'))
        .on('progress', p => { if (p.percent) process.stdout.write(`\r🎞  Converting: ${Math.round(p.percent)}%   `); })
        .on('end', () => { 
          process.stdout.write('\n'); 
          console.log('✅  Video converted (without delay cutting)');
          resolve(mp4Path); 
        })
        .on('error', err => reject(err))
        .save(mp4Path);
    });
  }
}
