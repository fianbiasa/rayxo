<?php

namespace App\Enums;

/**
 * Where a backup's copy on the remote destination stands.
 */
enum RemoteCopyStatus: string
{
    case Uploading = 'uploading';
    case Uploaded = 'uploaded';
    case Failed = 'failed';
    // The copy is being brought back to the server.
    case Fetching = 'fetching';
}
